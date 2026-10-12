package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Small-context models: compact early between turns (phase 1) and nudge the
// model to hand unfinished work to a subagent inside a turn (phase 2). See
// docs/plan-context-handoff.md.

const (
	defaultCompactAtPct = 60
	defaultHandoffAtPct = 65
)

// handoffNotice is appended once per turn when the context is nearly full.
const handoffNotice = "(system notice: your context is nearly full. Call spawn_agent now with a complete brief " +
	"(the goal, what is done, what is left, and exact file paths). Do not read more files yourself.)"

// envPercent reads a percentage from the environment. Unset or invalid gives
// def; 0 means off. Values above 100 are capped.
func envPercent(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return min(n, 100)
}

// overPercent reports whether msgs use more than pct percent of budget.
// pct 0 is off.
func overPercent(msgs []Message, budget, pct int) bool {
	return pct > 0 && estimateTokens(msgs)*100 > budget*pct
}

// compactModelFor is the model that writes summaries: GOHARNESS_COMPACT_MODEL
// when set (small models write weak summaries), else the session's model.
func compactModelFor(st Settings) string {
	if m := strings.TrimSpace(os.Getenv("GOHARNESS_COMPACT_MODEL")); m != "" {
		return m
	}
	return st.Model
}

// autoCompact compacts the earlier conversation before a turn starts when it
// has grown past GOHARNESS_COMPACT_AT percent of the prompt budget. Failure
// or nothing to compact leaves history unchanged and is not an error.
func (s *Session) autoCompact(ctx context.Context, sink func(Event)) {
	pct := envPercent("GOHARNESS_COMPACT_AT", defaultCompactAtPct)
	s.mu.Lock()
	skip := s.sub || s.running || pct == 0
	st := s.settings
	s.mu.Unlock()
	if skip {
		return
	}
	baseURL, apiKey, _ := s.resolveProvider(st.Provider)
	budget := promptBudget(modelContextTokens(baseURL, apiKey, st.Model))
	if !overPercent(s.Messages(), budget, pct) {
		return
	}
	res, err := s.Compact(ctx)
	if err != nil {
		return
	}
	sink(Event{Type: EvNotice, Text: fmt.Sprintf("compacted: %s -> %s tokens",
		formatTokens(res.TokensBefore), formatTokens(res.TokensAfter))})
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return strconv.Itoa(n)
}

// maybeHandoffNudge appends the handoff notice to the conversation when the
// context passes GOHARNESS_HANDOFF_AT percent of budget. The caller makes sure
// it fires at most once per turn and only where spawn_agent is available.
func (s *Session) maybeHandoffNudge(msgs []Message, budget int) bool {
	if !overPercent(msgs, budget, envPercent("GOHARNESS_HANDOFF_AT", defaultHandoffAtPct)) {
		return false
	}
	s.appendMessage(Message{Role: "user", Content: strPtr(handoffNotice)})
	return true
}

func offersTool(tools []ToolSchema, name string) bool {
	for _, t := range tools {
		if t.Function.Name == name {
			return true
		}
	}
	return false
}
