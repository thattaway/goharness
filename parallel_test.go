package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// parallelRig is a fake model for the parallel-subagent tests. The parent's first turn asks for one spawn_agent
// per brief at once. Each subagent's first request runs childWork, then makes a tool call (so it reports a step);
// its second request answers "answer <brief>". The parent's second turn ends the conversation.
func parallelRig(t *testing.T, briefs []string, childWork func(s *Session, brief string)) (*Session, *fakeLLM, *collector) {
	t.Helper()
	var calls []FunctionCall
	for _, b := range briefs {
		calls = append(calls, toolCall(spawnAgentTool, map[string]any{"task": b}))
	}
	var sess *Session
	llm, url := newFakeLLM(t, func(n int, req map[string]any) fakeReply {
		raw, _ := json.Marshal(req["messages"])
		text := string(raw)
		hasToolResult := strings.Contains(text, `"role":"tool"`)
		if strings.Contains(text, "read-only research subagent") {
			var brief string
			for _, b := range briefs {
				if strings.Contains(text, "Task: "+b) {
					brief = b
				}
			}
			if hasToolResult {
				return fakeReply{text: "answer " + brief}
			}
			childWork(sess, brief)
			return fakeReply{calls: []FunctionCall{toolCall("read_file", map[string]any{"path": "nothing-" + brief + ".md"})}}
		}
		if hasToolResult {
			return fakeReply{text: "all done"}
		}
		return fakeReply{calls: calls}
	})
	sess, _ = newTestSession(t, url)
	return sess, llm, &collector{}
}

func runRig(t *testing.T, s *Session, c *collector) {
	t.Helper()
	if err := s.Run(context.Background(), "investigate", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentsFromOneTurnRunAtTheSameTime(t *testing.T) {
	t.Setenv("GOHARNESS_PARALLEL", "")
	var arrived atomic.Int32
	var bothRan, statsSawTwo atomic.Bool
	s, llm, c := parallelRig(t, []string{"alpha", "beta"}, func(sess *Session, brief string) {
		arrived.Add(1)
		// Each subagent waits for the other to arrive; that can only happen if they truly run together.
		for deadline := time.Now().Add(5 * time.Second); arrived.Load() < 2 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if arrived.Load() >= 2 {
			bothRan.Store(true)
			if sess.Stats().SubActive == 2 {
				statsSawTwo.Store(true)
			}
		}
		if brief == "alpha" { // alpha finishes last, so result order is not finishing order
			time.Sleep(300 * time.Millisecond)
		}
	})
	runRig(t, s, c)
	if !bothRan.Load() {
		t.Fatal("the two subagents never ran at the same time")
	}
	if !statsSawTwo.Load() {
		t.Error("the session's stats never showed two subagents running")
	}
	results := toolResults(llm.request(len(llm.requests) - 1))
	if len(results) != 2 || !strings.Contains(results[0], "answer alpha") || !strings.Contains(results[1], "answer beta") {
		t.Fatalf("results must come back in the order the model asked for them, got %v", results)
	}
	if got := s.Stats().SubActive; got != 0 {
		t.Errorf("%d subagents still counted as running after the turn", got)
	}
}

func TestSubagentStepsAreTaggedWithTheirCall(t *testing.T) {
	t.Setenv("GOHARNESS_PARALLEL", "")
	s, _, c := parallelRig(t, []string{"alpha", "beta"}, func(*Session, string) { time.Sleep(50 * time.Millisecond) })
	runRig(t, s, c)
	byID := map[string][]string{}
	c.mu.Lock()
	for _, ev := range c.events {
		if ev.Type == EvNotice && strings.HasPrefix(ev.Text, "subagent: ") {
			byID[ev.ID] = append(byID[ev.ID], ev.Text)
		}
	}
	c.mu.Unlock()
	if len(byID) != 2 {
		t.Fatalf("expected step notices tagged with 2 different call ids, got %v", byID)
	}
	for id, steps := range byID {
		if id == "" || len(steps) != 1 || !strings.Contains(steps[0], "read_file") {
			t.Errorf("call %q: %v", id, steps)
		}
	}
}

func TestParallelLimitIsRespected(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		briefs      []string
		want        int32
	}{
		{"off", "1", []string{"a", "b", "c"}, 1},
		{"two", "2", []string{"a", "b", "c"}, 2},
		{"default", "", []string{"a", "b", "c", "d"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOHARNESS_PARALLEL", tc.limit)
			var inFlight, peak atomic.Int32
			s, _, c := parallelRig(t, tc.briefs, func(*Session, string) {
				n := inFlight.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				time.Sleep(150 * time.Millisecond)
				inFlight.Add(-1)
			})
			runRig(t, s, c)
			if peak.Load() != tc.want {
				t.Fatalf("most subagents running at once = %d, want %d", peak.Load(), tc.want)
			}
		})
	}
}

func TestParallelLimitSetting(t *testing.T) {
	for in, want := range map[string]int{"": 2, "1": 1, "3": 3, "9": maxParallelSubagents, "0": 1, "-4": 1, "abc": 2} {
		t.Setenv("GOHARNESS_PARALLEL", in)
		if got := parallelLimit(); got != want {
			t.Errorf("GOHARNESS_PARALLEL=%q gives %d, want %d", in, got, want)
		}
	}
}

func TestACancelledTurnStillAnswersEveryCall(t *testing.T) {
	t.Setenv("GOHARNESS_PARALLEL", "")
	ctx, cancel := context.WithCancel(context.Background())
	s, llm, c := parallelRig(t, []string{"a", "b"}, func(*Session, string) { cancel(); time.Sleep(100 * time.Millisecond) })
	_ = s.Run(ctx, "investigate", nil, c.sink, noApprover(t))
	// Whatever was in flight, every tool call the model made has a matching tool message.
	msgs := s.Messages()
	asked, answered := 0, 0
	for _, m := range msgs {
		asked += len(m.ToolCalls)
		if m.Role == "tool" {
			answered++
		}
	}
	if asked == 0 || asked != answered {
		t.Fatalf("%d tool calls but %d tool messages", asked, answered)
	}
	_ = llm
}
