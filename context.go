package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Keeping the conversation inside the model's context window. The full
// history stays in the session (and on disk); only the copy sent to the
// model is trimmed, oldest and bulkiest material first.

// defaultContextTokens is used when the server doesn't report a model's
// context length. Set from --context-tokens.
var defaultContextTokens = 32768

const (
	trimmedToolChars  = 400 // an old tool result is cut to this many characters
	trimNoteThreshold = 600 // only tool results longer than this are cut
	omittedNote       = "\n\n(Earlier parts of this conversation were omitted to fit the context window.)"
)

// estimateTokens is a rough count: ~3.5 characters per token plus a few
// tokens of per-message overhead. Good enough to stay clear of the limit.
func estimateTokens(msgs []Message) int {
	chars := 0
	for _, m := range msgs {
		chars += 16
		if m.Content != nil {
			chars += len(*m.Content)
		}
		for _, tc := range m.ToolCalls {
			chars += len(tc.Function.Name) + len(tc.Function.Arguments) + 16
		}
	}
	return chars * 2 / 7
}

// trimHistory returns msgs cut down to about budget tokens, and whether
// anything was cut. msgs[0] must be the system message. It first shortens
// long tool results from earlier turns, then drops whole early turns (so
// every tool call keeps its result), then shortens tool results in the
// current turn except the latest round's. The current turn's user message
// is always kept.
func trimHistory(msgs []Message, budget int) ([]Message, bool) {
	if estimateTokens(msgs) <= budget || len(msgs) < 2 {
		return msgs, false
	}
	out := append([]Message(nil), msgs...)

	// The current turn starts at the last real user message.
	current := len(out)
	for i := len(out) - 1; i > 0; i-- {
		if out[i].Role == "user" && !isSystemNotice(out[i]) {
			current = i
			break
		}
	}

	shorten := func(from, to int) {
		for i := from; i < to; i++ {
			// A loaded skill's instructions are the spec for the work in
			// progress; cutting them mid-task would derail it.
			if m := out[i]; m.Role == "tool" && m.Content != nil && len(*m.Content) > trimNoteThreshold && !isSkillInstructions(*m.Content) {
				out[i].Content = strPtr((*m.Content)[:trimmedToolChars] + "\n… (older tool output trimmed to save context)")
			}
		}
	}

	shorten(1, current)
	if estimateTokens(out) <= budget {
		return out, true
	}

	// Drop whole turns from the front until it fits.
	sys := out[0]
	rest := out[1:]
	current--
	dropped := false
	for estimateTokens(append([]Message{sys}, rest...)) > budget && current > 0 {
		next := 1
		for next < current && !(rest[next].Role == "user" && !isSystemNotice(rest[next])) {
			next++
		}
		rest = rest[next:]
		current -= next
		dropped = true
	}
	if dropped {
		sys.Content = strPtr(*sys.Content + omittedNote)
	}
	out = append([]Message{sys}, rest...)
	current++

	if estimateTokens(out) > budget {
		// Still too big: the current turn itself is long. Keep the latest
		// round of tool results intact and shorten the rest.
		lastAssistant := len(out)
		for i := len(out) - 1; i >= current; i-- {
			if out[i].Role == "assistant" {
				lastAssistant = i
				break
			}
		}
		shorten(current, lastAssistant)
	}
	return out, true
}

func isSkillInstructions(content string) bool {
	return strings.HasPrefix(content, skillInstructionsHeader)
}

func isSystemNotice(m Message) bool {
	return m.Content != nil && len(*m.Content) > 15 && (*m.Content)[:15] == "(system notice:"
}

// contextLengths caches what the provider's /models reports per model.
var contextLengths = struct {
	sync.Mutex
	byURL map[string]map[string]int
	at    map[string]time.Time
}{byURL: map[string]map[string]int{}, at: map[string]time.Time{}}

// modelContextTokens returns the context window for a model: from the
// provider's /models listing when it reports one (llama-swap does), else
// defaultContextTokens. apiKey is sent as a bearer token when non-empty
// (llama-swap doesn't need one; other providers may).
func modelContextTokens(baseURL, apiKey, model string) int {
	contextLengths.Lock()
	lengths, ok := contextLengths.byURL[baseURL]
	age := time.Since(contextLengths.at[baseURL])
	contextLengths.Unlock()
	// Refresh every 10 minutes, or after a minute if this model is unknown
	// (it may have just been added to the server).
	if !ok || age > 10*time.Minute || (lengths[model] == 0 && age > time.Minute) {
		lengths = fetchContextLengths(baseURL, apiKey)
		contextLengths.Lock()
		contextLengths.byURL[baseURL] = lengths
		contextLengths.at[baseURL] = time.Now()
		contextLengths.Unlock()
	}
	if n := lengths[model]; n > 0 {
		return n
	}
	return defaultContextTokens
}

func fetchContextLengths(baseURL, apiKey string) map[string]int {
	out := map[string]int{}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/models", nil)
	if err != nil {
		return out
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var data struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			MaxModelLen   int    `json:"max_model_len"` // vLLM
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return out
	}
	for _, d := range data.Data {
		n := d.ContextLength
		if n == 0 {
			n = d.MaxModelLen
		}
		out[d.ID] = n
	}
	return out
}

// promptBudget is how many tokens of history to send: three quarters of
// the window, leaving room for the tool schemas and the reply.
func promptBudget(contextTokens int) int {
	b := contextTokens*3/4 - 1500
	if b < 2000 {
		b = 2000
	}
	return b
}

func trimNotice(budget int) string {
	return fmt.Sprintf("older messages trimmed to fit the model's context (~%d tokens of history)", budget)
}

// visionModels caches which models the provider's /models listing marks as
// accepting images (llama-swap reports this per model as capabilities.vision).
// Same shape and refresh policy as contextLengths above, kept separate so a
// server that omits capabilities entirely (leaving every model false) can't
// also blank out the context-length cache.
var visionModels = struct {
	sync.Mutex
	byURL map[string]map[string]bool
	at    map[string]time.Time
}{byURL: map[string]map[string]bool{}, at: map[string]time.Time{}}

// modelSupportsImages reports whether model accepts image input, per the
// provider's /models listing. A provider that doesn't report capabilities
// at all (or is unreachable) makes every model report false, so an image
// send fails with a clear "switch models" error rather than a confusing
// one from the model server.
func modelSupportsImages(baseURL, apiKey, model string) bool {
	visionModels.Lock()
	models, ok := visionModels.byURL[baseURL]
	age := time.Since(visionModels.at[baseURL])
	visionModels.Unlock()
	if !ok || age > 10*time.Minute {
		models = fetchVisionCapabilities(baseURL, apiKey)
		visionModels.Lock()
		visionModels.byURL[baseURL] = models
		visionModels.at[baseURL] = time.Now()
		visionModels.Unlock()
	}
	return models[model]
}

func fetchVisionCapabilities(baseURL, apiKey string) map[string]bool {
	out := map[string]bool{}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/models", nil)
	if err != nil {
		return out
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var data struct {
		Data []struct {
			ID           string `json:"id"`
			Capabilities struct {
				Vision bool `json:"vision"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return out
	}
	for _, d := range data.Data {
		out[d.ID] = d.Capabilities.Vision
	}
	return out
}
