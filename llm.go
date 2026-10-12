package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Model      string     `json:"model,omitempty"` // which model produced this (assistant messages only) - display-only, not sent back to the API
	// Images are data: URLs attached to a user message. They ride alongside
	// Content (never inside it) and only reshape the outgoing request - see
	// apiPayloadMessages - for a vision model; everything else that reads
	// Content (trimming, secrets, transcripts, checkpoints) is unaffected.
	Images []string `json:"images,omitempty"`
}

// apiContentPart and apiImageURL are the OpenAI vision request shape: when a
// message carries images, "content" becomes an array of these instead of a
// plain string.
type apiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *apiImageURL `json:"image_url,omitempty"`
}

type apiImageURL struct {
	URL string `json:"url"`
}

// apiPayloadMessages converts the session's own Message records into the
// wire format sent to the model server. It matches how []Message used to be
// marshaled directly, except a message with Images gets an array-of-parts
// "content" (text part first, then one image_url part per image) instead of
// a plain string - Images itself is never sent as its own field.
func apiPayloadMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		entry := map[string]any{"role": m.Role}
		switch {
		case len(m.Images) > 0:
			var parts []apiContentPart
			if m.Content != nil && *m.Content != "" {
				parts = append(parts, apiContentPart{Type: "text", Text: *m.Content})
			}
			for _, url := range m.Images {
				parts = append(parts, apiContentPart{Type: "image_url", ImageURL: &apiImageURL{URL: url}})
			}
			entry["content"] = parts
		case m.Content != nil:
			entry["content"] = *m.Content
		default:
			entry["content"] = nil
		}
		if len(m.ToolCalls) > 0 {
			entry["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			entry["tool_call_id"] = m.ToolCallID
		}
		if m.Model != "" {
			entry["model"] = m.Model
		}
		out[i] = entry
	}
	return out
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSchema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

var toolSchemas = []ToolSchema{
	{Type: "function", Function: FunctionSchema{
		Name:        "bash",
		Description: "Run a shell command and return its combined stdout and stderr. " +
			"When rtk is installed, the output of common commands (git, ls, grep, docker, ...) is compressed to save context; " +
			"a note says when that happened. If compressed output is cut off or missing detail you need, run the command again with raw=true.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The shell command to run"},
				"raw":     map[string]any{"type": "boolean", "description": "Return the complete, unfiltered output instead of rtk's compressed version."},
			},
			"required": []string{"command"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "read_file",
		Description: "Read a file and return its contents.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file to read"},
			},
			"required": []string{"path"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "write_file",
		Description: "Create a file, or overwrite it if it already exists.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File to write"},
				"content": map[string]any{"type": "string", "description": "The full contents"},
			},
			"required": []string{"path", "content"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "str_replace",
		Description: "Replace exact text in a file. old_str must appear exactly once, so include surrounding lines if needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":             map[string]any{"type": "string", "description": "File to edit"},
				"old_str":          map[string]any{"type": "string", "description": "Exact text to find"},
				"new_str":          map[string]any{"type": "string", "description": "Text to put in its place"},
				"allow_multi_edit": map[string]any{"type": "boolean", "description": "Replace every match instead of failing"},
			},
			"required": []string{"path", "old_str", "new_str"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "tavily_search",
		Description: "Perform a web search using Tavily API.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string", "description": "The search query."},
				"max_results":  map[string]any{"type": "integer", "description": "Number of results (1-10, default 3)."},
				"search_depth": map[string]any{"type": "string", "enum": []string{"basic", "advanced"}, "description": "Use advanced for harder queries (slower)."},
			},
			"required": []string{"query"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "fetch_page",
		Description: "Fetch one web page and return its readable text as Markdown. Use it to read a specific URL (docs, an article, a search result). Long pages come back in chunks: pass offset to continue.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":       map[string]any{"type": "string", "description": "Full http(s) URL."},
				"mode":      map[string]any{"type": "string", "enum": []string{"auto", "http", "browser"}, "description": "auto (default) tries plain HTTP, then a headless browser if the page looks thin. browser forces JavaScript rendering."},
				"max_chars": map[string]any{"type": "integer", "description": "Characters to return (default 8000, max 30000)."},
				"offset":    map[string]any{"type": "integer", "description": "Character offset to start from, to read the next chunk."},
			},
			"required": []string{"url"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name:        "skill",
		Description: "Load an installed skill (listed under SKILLS in your instructions): its instructions, or one of its files such as a book chapter. Load a skill before doing a task it covers. Long files come back in chunks: pass offset to continue.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string", "description": "The skill's name, e.g. book-to-skill."},
				"file":   map[string]any{"type": "string", "description": "A file inside the skill, relative to its folder (e.g. chapters/ch03-replication.md). Omit to load the skill's instructions."},
				"offset": map[string]any{"type": "integer", "description": "Character offset to continue a long file from."},
			},
			"required": []string{"name"},
		},
	}},
	{Type: "function", Function: FunctionSchema{
		Name: spawnAgentTool,
		Description: "Hand a self-contained research task to a read-only subagent that works in a fresh context and returns only its final answer. " +
			"Use it for any review, audit, overview or \"how does X work\" question about a codebase, and whenever answering would take more than 2-3 files read or searched, " +
			"even in a small project: reading the files yourself fills your own context and slows every later turn. Read a file yourself only when you are about to edit it. " +
			"The subagent cannot edit files and does not see this conversation, so write the task as a complete brief: the absolute project path, what to find, and what to report " +
			"(exact values, with file:line quotes). Tell it to run ls -R first, then one grep with several keywords across the code folder, then open only the files grep points to, " +
			"and to say what it did not find instead of guessing. " +
			"When the work has independent parts (for example the API routes, the data layer and the worker), make one spawn_agent call per part in the SAME reply, not one per turn: " +
			"calls in one reply run at the same time, on different model servers, and all the answers come back together, which is much faster.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "The complete task brief for the subagent."},
			},
			"required": []string{"task"},
		},
	}},
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// Reasoning ("thinking") text: llama.cpp and vLLM use
			// reasoning_content, some other servers use reasoning.
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	// Usage arrives on its own final chunk (empty Choices) when the
	// request sets stream_options.include_usage - confirmed against this
	// fleet's own llama.cpp server response shape.
	Usage *Usage `json:"usage"`
}

// Usage is one API call's token accounting, in the shape llama.cpp (and
// OpenAI-compatible servers generally) report it.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type StreamResult struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	// TTFT is how long the first byte of content or reasoning took to
	// arrive after the request was sent; zero if nothing ever streamed.
	TTFT time.Duration
	// GenDuration spans from the first streamed byte to the last - the
	// Endpoint is the backend host that served the request, as named by
	// Olla's X-Olla-Endpoint header with its "-llama-swap" suffix dropped;
	// empty when the server doesn't say.
	Endpoint string
	// denominator for tokens/sec, excluding time-to-first-token so a slow
	// model load doesn't make generation itself look slow.
	GenDuration time.Duration
}

// planTools and fullTools let callers pick which tool schemas to offer the
// model for a given round - nil means no tools at all (forces a text-only
// close-out once the round cap is hit). Plan mode offers no write tools.
var planTools = []ToolSchema{toolSchemas[0], toolSchemas[1], toolSchemas[4], toolSchemas[5], toolSchemas[6], toolSchemas[7]} // bash, read_file, tavily_search, fetch_page, skill, spawn_agent
var fullTools = toolSchemas

// streamIdleTimeout ends a response that has stopped sending anything. It
// is a var so tests can shorten it.
var streamIdleTimeout = 5 * time.Minute

// errStreamIdle is returned when the model server goes quiet mid-response.
var errStreamIdle = errors.New("the model server stopped responding (nothing received for a while); it may be busy or stuck")

// streamHooks receive a response as it streams in. Any may be nil.
type streamHooks struct {
	Content   func(string)                 // answer text
	Reasoning func(string)                 // thinking text, not part of the answer
	ToolStart func(index int, name string) // a tool call has begun (arguments still streaming)
}

// defaultMaxTokens caps how much a single reply asks for. Without an
// explicit max_tokens, some providers default to "as much as the model's
// full output capacity allows" - harmless on llama-swap (local, no cost),
// but on a credit-metered provider that can mean a request for far more
// tokens than the account can actually afford, failing outright even
// though a short reply would have fit easily (observed: a 65536-token
// implicit request against an OpenRouter free-tier balance that could
// only cover 6233, going through freellmapi). 4096 comfortably covers a
// normal reply; a task that genuinely needs more keeps going across
// further tool-calling rounds rather than being cut off mid-answer.
const defaultMaxTokens = 4096

// streamChat posts a chat completion request and streams the response
// through hooks. It returns the answer text (reasoning excluded) and any
// tool calls the model requested. Pass a nil tools slice to omit tools.
func streamChat(ctx context.Context, baseURL, apiKey, model string, messages []Message, tools []ToolSchema, hooks streamHooks) (StreamResult, error) {
	payload := map[string]any{
		"model":          model,
		"messages":       apiPayloadMessages(messages),
		"stream":         true,
		"max_tokens":     defaultMaxTokens,
		"stream_options": map[string]any{"include_usage": true},
	}
	if tools != nil {
		payload["tools"] = tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return StreamResult{}, err
	}

	// Cancel the request if the stream goes quiet for streamIdleTimeout.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var idle atomic.Bool
	watchdog := time.AfterFunc(streamIdleTimeout, func() { idle.Store(true); cancel() })
	defer watchdog.Stop()
	fail := func(err error) (StreamResult, error) {
		if idle.Load() {
			return StreamResult{}, errStreamIdle
		}
		return StreamResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return StreamResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// No overall Timeout: it would cap the whole streamed body, cutting off any
	// answer longer than the limit. Wait long for the first byte (llama-swap may
	// be loading a model); ctx still cancels the stream.
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 5 * time.Minute,
	}}
	watchdog.Reset(5*time.Minute + streamIdleTimeout)
	sentAt := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	watchdog.Reset(streamIdleTimeout)

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errorBody)
		return StreamResult{}, fmt.Errorf("model server returned status %d: %v", resp.StatusCode, errorBody)
	}

	type accCall struct {
		id, name, args string
	}
	calls := map[int]*accCall{}
	var contentBuf bytes.Buffer
	var firstByteAt, lastByteAt time.Time
	var usage Usage
	emit := func(f func(string), s string) {
		if s == "" {
			return
		}
		now := time.Now()
		if firstByteAt.IsZero() {
			firstByteAt = now
		}
		lastByteAt = now
		if f != nil {
			f(s)
		}
	}
	think := thinkSplitter{
		content:   func(s string) { contentBuf.WriteString(s); emit(hooks.Content, s) },
		reasoning: func(s string) { emit(hooks.Reasoning, s) },
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		watchdog.Reset(streamIdleTimeout)
		line := scanner.Text()
		if len(line) < 6 || line[:6] != "data: " {
			continue
		}
		raw := line[6:]
		if raw == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		emit(hooks.Reasoning, delta.ReasoningContent)
		emit(hooks.Reasoning, delta.Reasoning)
		think.write(delta.Content)
		for _, tc := range delta.ToolCalls {
			entry, ok := calls[tc.Index]
			if !ok {
				entry = &accCall{}
				calls[tc.Index] = entry
			}
			if tc.ID != "" {
				entry.id = tc.ID
			}
			if tc.Function.Name != "" {
				started := entry.name == ""
				entry.name += tc.Function.Name
				if started && hooks.ToolStart != nil {
					hooks.ToolStart(tc.Index, entry.name)
				}
			}
			if tc.Function.Arguments != "" {
				entry.args += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	if idle.Load() {
		return StreamResult{}, errStreamIdle
	}
	think.flush()

	var indices []int
	for i := range calls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	var toolCalls []ToolCall
	for i, idx := range indices {
		c := calls[idx]
		id := c.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:   id,
			Type: "function",
			Function: FunctionCall{
				Name:      c.name,
				Arguments: c.args,
			},
		})
	}

	result := StreamResult{Content: strings.TrimLeft(contentBuf.String(), "\n"), ToolCalls: toolCalls, Usage: usage,
		Endpoint: strings.TrimSuffix(resp.Header.Get("X-Olla-Endpoint"), "-llama-swap")}
	if !firstByteAt.IsZero() {
		result.TTFT = firstByteAt.Sub(sentAt)
		result.GenDuration = lastByteAt.Sub(firstByteAt)
	}
	return result, nil
}

// thinkSplitter routes <think>...</think> sections of streamed content to
// reasoning, for models whose chat template puts their thinking inline.
// A tag may be split across chunks, so a trailing partial tag is held back
// until the next chunk shows whether it really is one.
type thinkSplitter struct {
	content, reasoning func(string)
	inThink            bool
	pending            string
}

func (t *thinkSplitter) write(s string) {
	s = t.pending + s
	t.pending = ""
	for s != "" {
		tag, out := "<think>", t.content
		if t.inThink {
			tag, out = "</think>", t.reasoning
		}
		if i := strings.Index(s, tag); i >= 0 {
			out(s[:i])
			s = s[i+len(tag):]
			t.inThink = !t.inThink
			continue
		}
		// Hold back a suffix that could be the start of the tag.
		keep := 0
		for n := len(tag) - 1; n > 0; n-- {
			if strings.HasSuffix(s, tag[:n]) {
				keep = n
				break
			}
		}
		out(s[:len(s)-keep])
		t.pending = s[len(s)-keep:]
		return
	}
}

func (t *thinkSplitter) flush() {
	if t.pending == "" {
		return
	}
	if t.inThink {
		t.reasoning(t.pending)
	} else {
		t.content(t.pending)
	}
	t.pending = ""
}
