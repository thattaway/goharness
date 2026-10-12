package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// compactSummaryPrefix starts the message that stands in for the compacted
// part of the conversation. It is a user message (not system) so any chat
// template accepts it anywhere, followed by a short assistant acknowledgement
// to keep roles alternating into the kept turn.
const compactSummaryPrefix = "(summary of the earlier conversation, which was compacted to save context)\n\n"

const compactAck = "Understood. I'll continue from that summary."

const compactSystemPrompt = "You summarize a conversation between a user and a coding assistant so the assistant can " +
	"continue the work later without the original messages. Write a compact, factual summary: what the user wants, " +
	"decisions made and why, files and commands that mattered and what they showed, what is done, what is still open, " +
	"and any exact names, paths, numbers or error messages that will be needed again. Do not add advice or new ideas. " +
	"Reply with the summary only."

// Per-message and total caps on the transcript sent to the summarizer, in
// characters, so compacting a huge conversation can't overflow the model.
const (
	compactToolChars  = 1500
	compactTotalChars = 60000
)

type CompactResult struct {
	MessagesBefore, MessagesAfter int
	TokensBefore, TokensAfter     int // estimates
}

func isCompactSummary(m Message) bool {
	return m.Role == "user" && m.Content != nil && strings.HasPrefix(*m.Content, compactSummaryPrefix)
}

// Compact replaces everything before the latest user message with a
// model-written summary, so the conversation keeps its thread in far fewer
// tokens. The latest turn is kept verbatim. Turn marks older than the kept
// turn are dropped (those turns can no longer be undone); the rest are
// re-pointed. Nothing changes if the summary can't be produced.
func (s *Session) Compact(ctx context.Context) (CompactResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return CompactResult{}, ErrBusy
	}
	msgs := append([]Message(nil), s.messages...)
	st := s.settings
	current := -1
	for i := len(msgs) - 1; i > 0; i-- {
		if msgs[i].Role == "user" && !isSystemNotice(msgs[i]) && !isCompactSummary(msgs[i]) {
			current = i
			break
		}
	}
	if current < 2 || (current == 3 && isCompactSummary(msgs[1])) {
		s.mu.Unlock()
		return CompactResult{}, errors.New("nothing to compact yet: there is no earlier conversation")
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	baseURL, apiKey, _ := s.resolveProvider(st.Provider)
	result, err := streamChat(ctx, baseURL, apiKey, st.Model, []Message{
		{Role: "system", Content: strPtr(compactSystemPrompt)},
		{Role: "user", Content: strPtr("Conversation to summarize:\n\n" + compactTranscript(msgs[1:current]))},
	}, nil, streamHooks{})
	if err != nil {
		return CompactResult{}, fmt.Errorf("could not summarize: %w", err)
	}
	summary := strings.TrimSpace(result.Content)
	if summary == "" {
		return CompactResult{}, errors.New("the model returned an empty summary; nothing was changed")
	}
	s.recordUsage(result.Usage, result.TTFT, result.GenDuration)

	kept := msgs[current:]
	out := append([]Message{
		msgs[0],
		{Role: "user", Content: strPtr(compactSummaryPrefix + summary)},
		{Role: "assistant", Content: strPtr(compactAck), Model: st.Model},
	}, kept...)
	shift := 3 - current

	s.mu.Lock()
	defer s.mu.Unlock()
	var marks []turnMark
	for _, m := range s.turns {
		if m.MsgIndex >= current {
			m.MsgIndex += shift
			marks = append(marks, m)
		}
	}
	s.turns = marks
	s.messages = out
	saveSession(s.path, s.messages)
	s.saveMetaLocked()
	return CompactResult{
		MessagesBefore: len(msgs), MessagesAfter: len(out),
		TokensBefore: estimateTokens(msgs), TokensAfter: estimateTokens(out),
	}, nil
}

// compactTranscript renders messages as plain text for the summarizer. Tool
// calls and results are included but cut short.
func compactTranscript(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		text := ""
		if m.Content != nil {
			text = *m.Content
		}
		switch m.Role {
		case "tool":
			if len(text) > compactToolChars {
				text = text[:compactToolChars] + "… (cut)"
			}
			fmt.Fprintf(&b, "[tool result]\n%s\n\n", text)
		case "assistant":
			if text != "" {
				fmt.Fprintf(&b, "[assistant]\n%s\n", text)
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if len(args) > 400 {
					args = args[:400] + "…"
				}
				fmt.Fprintf(&b, "[assistant called %s %s]\n", tc.Function.Name, args)
			}
			b.WriteString("\n")
		default:
			fmt.Fprintf(&b, "[%s]\n%s\n\n", m.Role, text)
		}
	}
	out := b.String()
	if len(out) > compactTotalChars {
		out = "(the start of the conversation is omitted)\n…" + out[len(out)-compactTotalChars:]
	}
	return out
}
