package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func msg(role, content string) Message { return Message{Role: role, Content: strPtr(content)} }

// turn builds user -> assistant(tool call) -> tool result -> assistant.
func turn(n int, toolOut string) []Message {
	id := "c" + string(rune('a'+n))
	return []Message{
		msg("user", "question "+string(rune('A'+n))),
		{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: id, Content: strPtr(toolOut)},
		msg("assistant", "answer "+string(rune('A'+n))),
	}
}

func TestTrimHistoryUnderBudget(t *testing.T) {
	msgs := append([]Message{msg("system", "sys")}, turn(0, "short")...)
	out, cut := trimHistory(msgs, 10000)
	if cut || len(out) != len(msgs) {
		t.Fatal("trimmed a history that fits")
	}
}

func TestTrimHistoryShortensOldToolOutputFirst(t *testing.T) {
	big := strings.Repeat("x", 20000)
	msgs := []Message{msg("system", "sys")}
	msgs = append(msgs, turn(0, big)...)
	msgs = append(msgs, turn(1, "recent")...)
	msgs = append(msgs, msg("user", "now"))

	out, cut := trimHistory(msgs, 1500)
	if !cut || len(out) != len(msgs) {
		t.Fatalf("cut=%v len=%d, want the same messages with shorter content", cut, len(out))
	}
	if n := len(*out[3].Content); n > 500 {
		t.Fatalf("old tool output still %d chars", n)
	}
	if *msgs[3].Content != big {
		t.Fatal("trimHistory modified the caller's messages")
	}
}

func TestTrimHistoryDropsWholeOldTurns(t *testing.T) {
	msgs := []Message{msg("system", "sys")}
	for i := 0; i < 20; i++ {
		msgs = append(msgs, turn(i, strings.Repeat("y", 500))...)
	}
	msgs = append(msgs, msg("user", "latest question"))

	out, cut := trimHistory(msgs, 400)
	if !cut || out[0].Role != "system" || !strings.Contains(*out[0].Content, "omitted") {
		t.Fatalf("expected a trimmed history with a note in the system prompt")
	}
	if last := out[len(out)-1]; *last.Content != "latest question" {
		t.Fatalf("lost the current question: %+v", last)
	}
	if out[1].Role != "user" {
		t.Fatalf("history after the system prompt starts with %s, want a whole turn", out[1].Role)
	}
	// Every tool result still follows the call that produced it.
	calls := map[string]bool{}
	for _, m := range out {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == "tool" && !calls[m.ToolCallID] {
			t.Fatalf("orphaned tool result %s", m.ToolCallID)
		}
	}
	if estimateTokens(out) > 400 {
		t.Fatalf("still %d tokens", estimateTokens(out))
	}
}

func TestTrimHistoryKeepsLatestRoundOfCurrentTurn(t *testing.T) {
	big := strings.Repeat("z", 8000)
	msgs := []Message{msg("system", "sys"), msg("user", "do it")}
	for i := 0; i < 3; i++ {
		id := "r" + string(rune('0'+i))
		msgs = append(msgs,
			Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Function: FunctionCall{Name: "bash"}}}},
			Message{Role: "tool", ToolCallID: id, Content: strPtr(big)})
	}
	out, _ := trimHistory(msgs, 3000)
	if *out[len(out)-1].Content != big {
		t.Fatal("latest tool result was shortened")
	}
	if len(*out[3].Content) > 500 {
		t.Fatal("earlier tool result in the current turn was not shortened")
	}
}

func TestFetchVisionCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"vision-model","capabilities":{"vision":true}},{"id":"text-model","capabilities":{"vision":false}},{"id":"no-caps"}]}`)
	}))
	t.Cleanup(srv.Close)
	got := fetchVisionCapabilities(srv.URL, "")
	if !got["vision-model"] || got["text-model"] || got["no-caps"] {
		t.Fatalf("%#v", got)
	}
}

func TestModelSupportsImagesCaches(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"data":[{"id":"m","capabilities":{"vision":true}}]}`)
	}))
	t.Cleanup(srv.Close)
	if !modelSupportsImages(srv.URL, "", "m") {
		t.Fatal("expected true")
	}
	if !modelSupportsImages(srv.URL, "", "m") {
		t.Fatal("expected true (cached)")
	}
	if hits != 1 {
		t.Fatalf("hit the server %d times, wanted 1 (cache not honored)", hits)
	}
	if modelSupportsImages(srv.URL, "", "unknown-model") {
		t.Fatal("unknown model should be false")
	}
}

func TestTrimHistoryKeepsSkillInstructions(t *testing.T) {
	skill := skillInstructionsHeader + "big\n" + strings.Repeat("instruction ", 2000)
	msgs := []Message{msg("system", "sys"), msg("user", "/big-skill go")}
	msgs = append(msgs,
		Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "s", Function: FunctionCall{Name: "skill"}}}},
		Message{Role: "tool", ToolCallID: "s", Content: strPtr(skill)},
		Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "b", Function: FunctionCall{Name: "bash"}}}},
		Message{Role: "tool", ToolCallID: "b", Content: strPtr(strings.Repeat("output ", 3000))},
		Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Function: FunctionCall{Name: "bash"}}}},
		Message{Role: "tool", ToolCallID: "c", Content: strPtr("latest")},
	)
	out, _ := trimHistory(msgs, 3000)
	if *out[3].Content != skill {
		t.Fatal("skill instructions were trimmed")
	}
	if len(*out[5].Content) > 500 {
		t.Fatal("ordinary tool output in the current turn was not trimmed")
	}
}

func TestFetchContextLengthsSendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"data":[{"id":"m","context_length":9999}]}`)
	}))
	t.Cleanup(srv.Close)

	lengths := fetchContextLengths(srv.URL, "secret-key")
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-key")
	}
	if lengths["m"] != 9999 {
		t.Errorf("lengths[m] = %d", lengths["m"])
	}
}
