package main

import (
	"context"
	"os"
	"strings"
)

// spawn_agent: a read-only subagent for self-contained research. It runs the
// normal agent loop in a fresh Session - its own conversation, nothing from
// the parent's - so exploring a codebase doesn't fill the parent's context;
// only the subagent's final answer comes back as the tool result.
//
// It is read-only by construction: it gets Plan mode's tools (no writes) and
// can't ask for approval, so any command outside the allow-list is refused
// rather than shown to the user. It is also never offered spawn_agent itself.

const (
	spawnAgentTool    = "spawn_agent"
	subagentMaxRounds = 8
)

const subagentPreamble = "You are a read-only research subagent working for another agent. " +
	"You cannot edit files, and commands that need approval are refused. " +
	"Investigate the task below with the tools you have, then reply with a concise, self-contained answer: " +
	"what you found, with exact file paths and line references where useful. " +
	"The agent that asked cannot see your work, only your final reply."

// subagentModel is the model subagents use: GOHARNESS_SUBAGENT_MODEL if set,
// else the parent's. A smaller model here keeps the parent's host from having
// to swap models while a subagent runs.
func subagentModel(parent string) string {
	if m := strings.TrimSpace(os.Getenv("GOHARNESS_SUBAGENT_MODEL")); m != "" {
		return m
	}
	return parent
}

// subagentModelFor is the model a subagent started from a session with
// settings st should use: the session's own /subagent choice, then
// GOHARNESS_SUBAGENT_MODEL, then the session's model.
func subagentModelFor(st Settings) string {
	if m := strings.TrimSpace(st.SubagentModel); m != "" {
		return m
	}
	return subagentModel(st.Model)
}

func withoutTool(tools []ToolSchema, name string) []ToolSchema {
	var out []ToolSchema
	for _, t := range tools {
		if t.Function.Name != name {
			out = append(out, t)
		}
	}
	return out
}

func denyAll(context.Context, ToolCall, string) (bool, error) { return false, nil }

// runSubagent runs one subagent to completion and returns its answer, or a
// short error description, as the spawn_agent tool result.
func (s *Session) runSubagent(ctx context.Context, callID string, args map[string]any, sink func(Event)) string {
	if s.sub {
		return "Error: spawn_agent is not available inside a subagent."
	}
	task, _ := args["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return "Error: 'task' is required: write a complete brief for the subagent."
	}

	s.mu.Lock()
	parent := s.settings
	s.mu.Unlock()

	f, err := os.CreateTemp("", "goharness-subagent-*.json")
	if err != nil {
		return "Error: could not start a subagent: " + err.Error()
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	child := &Session{
		BaseURL:     s.BaseURL,
		ProjectRoot: s.ProjectRoot,
		path:        path,
		sub:         true,
		settings: Settings{
			Model:     subagentModelFor(parent),
			Provider:  parent.Provider,
			Plan:      true,
			MaxRounds: subagentMaxRounds,
			NoSkills:  true,
		},
	}
	s.subagentStarted(child.settings.Model)
	defer s.subagentFinished()
	child.messages = withSystemPrompt(nil, s.ProjectRoot, child.settings)
	child.messages = append(child.messages, Message{Role: "user", Content: strPtr(subagentPreamble + "\n\nTask: " + task)})

	// Surface the subagent's tool use as one-line notices so a long run isn't
	// silent; its text and results stay out of the parent's view.
	childSink := func(ev Event) {
		if ev.Type != EvToolCall {
			return
		}
		s.subagentTool()
		a := ev.Args
		if len(a) > 120 {
			a = a[:120] + "..."
		}
		sink(Event{Type: EvNotice, ID: callID, Text: "subagent: " + ev.Name + " " + a})
	}

	err = child.loop(ctx, childSink, denyAll)

	// Fold the subagent's usage into the parent's stats so the sidebar
	// reflects everything the session actually spent.
	s.mu.Lock()
	s.stats.promptTokens += child.stats.promptTokens
	s.stats.completionTokens += child.stats.completionTokens
	s.stats.cachedTokens += child.stats.cachedTokens
	s.stats.toolCalls += child.stats.toolCalls
	s.mu.Unlock()

	if err != nil {
		if ctx.Err() != nil {
			return "Cancelled."
		}
		return "Subagent failed: " + err.Error()
	}
	msgs := child.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != nil && strings.TrimSpace(*msgs[i].Content) != "" {
			return capOutput(*msgs[i].Content)
		}
	}
	return "The subagent finished without producing an answer."
}
