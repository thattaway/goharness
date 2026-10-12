package main

import (
	"context"
	"strings"
	"testing"
)

func smallContext(t *testing.T) {
	t.Helper()
	old := defaultContextTokens
	defaultContextTokens = 4096 // prompt budget bottoms out at 2000 tokens
	t.Cleanup(func() { defaultContextTokens = old })
}

func bigLLM(t *testing.T) (*fakeLLM, *Session) {
	llm, url := newFakeLLM(t, func(n int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		if first, _ := msgs[0].(map[string]any)["content"].(string); strings.HasPrefix(first, "You summarize") {
			return fakeReply{text: "SUMMARY of earlier turns"}
		}
		return fakeReply{text: strings.Repeat("word ", 700)}
	})
	s, _ := newTestSession(t, url)
	return llm, s
}

func runTurns(t *testing.T, s *Session, c *collector, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if err := s.Run(context.Background(), q, nil, c.sink, noApprover(t)); err != nil {
			t.Fatal(err)
		}
	}
}

func hasSummary(s *Session) bool {
	for _, m := range s.Messages() {
		if isCompactSummary(m) {
			return true
		}
	}
	return false
}

func TestAutoCompactRunsBeforeATurnOverTheThreshold(t *testing.T) {
	smallContext(t)
	t.Setenv("GOHARNESS_COMPACT_AT", "60")
	_, s := bigLLM(t)
	var c collector
	runTurns(t, s, &c, "one", "two", "three")
	if !hasSummary(s) {
		t.Fatal("history should have been compacted before the third turn")
	}
	found := false
	for _, ev := range c.find(EvNotice) {
		found = found || strings.HasPrefix(ev.Text, "compacted: ")
	}
	if !found {
		t.Fatalf("no compacted notice in %+v", c.events)
	}
}

func TestAutoCompactOff(t *testing.T) {
	smallContext(t)
	t.Setenv("GOHARNESS_COMPACT_AT", "0")
	_, s := bigLLM(t)
	var c collector
	runTurns(t, s, &c, "one", "two", "three")
	if hasSummary(s) {
		t.Fatal("GOHARNESS_COMPACT_AT=0 must turn auto-compact off")
	}
}

func TestAutoCompactNoEarlierTurnIsANoOp(t *testing.T) {
	smallContext(t)
	t.Setenv("GOHARNESS_COMPACT_AT", "1")
	llm, s := bigLLM(t)
	var c collector
	runTurns(t, s, &c, "one", "two")
	if hasSummary(s) {
		t.Fatal("nothing earlier than the kept turn, so nothing to compact")
	}
	if len(llm.requests) != 2 {
		t.Fatalf("expected only the 2 turn requests, got %d", len(llm.requests))
	}
}

func TestAutoCompactFailureLeavesHistory(t *testing.T) {
	smallContext(t)
	t.Setenv("GOHARNESS_COMPACT_AT", "1")
	_, s := bigLLM(t)
	var c collector
	runTurns(t, s, &c, "one", "two")
	before := s.Messages()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the summary request fails
	s.autoCompact(ctx, c.sink)
	after := s.Messages()
	if len(after) != len(before) || hasSummary(s) {
		t.Fatalf("history changed after a failed summary: %d -> %d", len(before), len(after))
	}
}

func TestEnvPercent(t *testing.T) {
	for val, want := range map[string]int{"": 60, "0": 0, "45": 45, "250": 100, "x": 60, "-3": 60} {
		t.Setenv("GOHARNESS_TEST_PCT", val)
		if got := envPercent("GOHARNESS_TEST_PCT", 60); got != want {
			t.Errorf("%q: got %d want %d", val, got, want)
		}
	}
}

func nudgeCount(req map[string]any) int {
	n := 0
	for _, m := range req["messages"].([]any) {
		if c, _ := m.(map[string]any)["content"].(string); c == handoffNotice {
			n++
		}
	}
	return n
}

func handoffRun(t *testing.T, sub bool) (*fakeLLM, *collector) {
	t.Setenv("GOHARNESS_HANDOFF_AT", "1")
	t.Setenv("GOHARNESS_COMPACT_AT", "0")
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n < 3 {
			return fakeReply{calls: []FunctionCall{toolCall("bash", map[string]any{"command": "echo hi"})}}
		}
		return fakeReply{text: "done"}
	})
	s, _ := newTestSession(t, url)
	s.sub = sub
	var c collector
	if err := s.Run(context.Background(), "do a long job", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	return llm, &c
}

func TestHandoffNudgeFiresOncePerTurn(t *testing.T) {
	llm, c := handoffRun(t, false)
	if nudgeCount(llm.request(0)) != 1 || nudgeCount(llm.request(2)) != 1 {
		t.Fatalf("the notice should be present once: %d, %d", nudgeCount(llm.request(0)), nudgeCount(llm.request(2)))
	}
	n := 0
	for _, ev := range c.find(EvNotice) {
		if strings.Contains(ev.Text, "hand off to a subagent") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected 1 notice event, got %d", n)
	}
}

func TestHandoffNudgeNotForSubagents(t *testing.T) {
	llm, _ := handoffRun(t, true)
	if nudgeCount(llm.request(0)) != 0 {
		t.Fatal("subagents must not be told to spawn_agent")
	}
}

func TestHandoffNudgeNeedsSpawnAgent(t *testing.T) {
	if offersTool(withoutTool(fullTools, spawnAgentTool), spawnAgentTool) || !offersTool(fullTools, spawnAgentTool) {
		t.Fatal("offersTool should reflect whether spawn_agent is offered")
	}
}
