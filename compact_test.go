package main

import (
	"context"
	"strings"
	"testing"
)

// twoTurnSession runs two turns against a fake model and returns the session
// and the fake, whose third reply is the summary Compact asks for.
func twoTurnSession(t *testing.T, summary string) (*Session, *fakeLLM) {
	t.Helper()
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{text: "answer to the first question"}
		case 2:
			return fakeReply{text: "answer to the second question"}
		}
		return fakeReply{text: summary}
	})
	s, _ := newTestSession(t, url)
	var c collector
	for _, q := range []string{"first question about alpha", "second question about beta"} {
		if err := s.Run(context.Background(), q, nil, c.sink, noApprover(t)); err != nil {
			t.Fatal(err)
		}
	}
	return s, llm
}

func TestCompactReplacesEarlierTurnsWithASummary(t *testing.T) {
	s, llm := twoTurnSession(t, "SUMMARY: the user asked about alpha")
	before := len(s.Messages())
	res, err := s.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages()
	if len(msgs) >= before+2 || res.MessagesBefore != before || res.MessagesAfter != len(msgs) {
		t.Fatalf("before=%d after=%d res=%+v", before, len(msgs), res)
	}
	var all []string
	for _, m := range msgs {
		if m.Content != nil {
			all = append(all, m.Role+": "+*m.Content)
		}
	}
	joined := strings.Join(all, "\n")
	if !strings.Contains(joined, "SUMMARY: the user asked about alpha") {
		t.Fatalf("summary missing:\n%s", joined)
	}
	if strings.Contains(joined, "first question about alpha") || strings.Contains(joined, "answer to the first question") {
		t.Fatalf("earlier turn should be gone:\n%s", joined)
	}
	if !strings.Contains(joined, "second question about beta") || !strings.Contains(joined, "answer to the second question") {
		t.Fatalf("the latest turn must be kept intact:\n%s", joined)
	}
	if msgs[0].Role != "system" || msgs[1].Role != "user" || msgs[2].Role != "assistant" || msgs[3].Role != "user" {
		t.Fatalf("roles must alternate after the system prompt, got %s %s %s %s", msgs[0].Role, msgs[1].Role, msgs[2].Role, msgs[3].Role)
	}
	// the summarizer saw the old conversation and no tools were offered to it
	req := llm.request(2)
	body := ""
	for _, m := range req["messages"].([]any) {
		body += m.(map[string]any)["content"].(string) + "\n"
	}
	if !strings.Contains(body, "first question about alpha") {
		t.Fatalf("summarizer request lacked the old turn:\n%s", body)
	}
	if _, has := req["tools"]; has {
		t.Fatal("the summarizer must not be offered tools")
	}
}

func TestCompactKeepsUndoWorkingForTheKeptTurn(t *testing.T) {
	s, _ := twoTurnSession(t, "SUMMARY")
	if len(s.turns) != 2 {
		t.Fatalf("setup: %d turn marks", len(s.turns))
	}
	if _, err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.turns) != 1 {
		t.Fatalf("the compacted turn can't be undone any more; marks = %d", len(s.turns))
	}
	msgs := s.Messages()
	idx := s.turns[0].MsgIndex
	if idx >= len(msgs) || msgs[idx].Role != "user" || *msgs[idx].Content != "second question about beta" {
		t.Fatalf("turn mark points at %d, which is not the kept user message", idx)
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if got := s.Messages(); len(got) != idx {
		t.Fatalf("undo left %d messages, want %d", len(got), idx)
	}
}

func TestCompactWithNothingEarlierIsRefused(t *testing.T) {
	_, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "only turn", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	before := len(s.Messages())
	if _, err := s.Compact(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
		t.Fatalf("err = %v", err)
	}
	if len(s.Messages()) != before {
		t.Fatal("history changed although compaction was refused")
	}
}

func TestCompactFailureLeavesHistoryAlone(t *testing.T) {
	s, _ := twoTurnSession(t, "") // an empty summary is a failure
	before := s.Messages()
	if _, err := s.Compact(context.Background()); err == nil {
		t.Fatal("expected an error for an empty summary")
	}
	after := s.Messages()
	if len(after) != len(before) || len(s.turns) != 2 {
		t.Fatalf("history changed on failure: %d -> %d messages, %d marks", len(before), len(after), len(s.turns))
	}
}

func TestCompactIsRefusedWhileARunIsGoing(t *testing.T) {
	s, _ := twoTurnSession(t, "SUMMARY")
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	if _, err := s.Compact(context.Background()); err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestCompactIsSavedToDisk(t *testing.T) {
	s, _ := twoTurnSession(t, "SUMMARY ON DISK")
	if _, err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSession(s.Path())
	if err != nil || len(saved) != len(s.Messages()) {
		t.Fatalf("saved %d messages, err %v; want %d", len(saved), err, len(s.Messages()))
	}
}

func TestCompactCommandIsListed(t *testing.T) {
	for _, c := range slashCommands {
		if c.name == "/compact" {
			return
		}
	}
	t.Fatal("/compact is not in slashCommands")
}
