package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeReply is one scripted model response: text, tool calls, or both.
type fakeReply struct {
	text  string
	calls []FunctionCall
}

// fakeLLM serves an OpenAI-compatible streaming /chat/completions endpoint.
// next picks the reply for each request and can inspect what was sent.
type fakeLLM struct {
	mu       sync.Mutex
	requests []map[string]any
	next     func(n int, req map[string]any) fakeReply

	endpoint         string // sent as X-Olla-Endpoint when set
	completionTokens int    // reported in a final usage chunk when set
}

func newFakeLLM(t *testing.T, next func(n int, req map[string]any) fakeReply) (*fakeLLM, string) {
	f := &fakeLLM{next: next}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"small-ctx","context_length":4096}]}`)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.requests = append(f.requests, req)
		n := len(f.requests)
		f.mu.Unlock()
		reply := f.next(n, req)

		w.Header().Set("Content-Type", "text/event-stream")
		if f.endpoint != "" {
			w.Header().Set("X-Olla-Endpoint", f.endpoint)
		}
		send := func(delta map[string]any) {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		for _, word := range strings.SplitAfter(reply.text, " ") {
			if word != "" {
				send(map[string]any{"content": word})
			}
		}
		for i, c := range reply.calls {
			send(map[string]any{"tool_calls": []any{map[string]any{
				"index": i, "id": fmt.Sprintf("call_%d_%d", n, i),
				"function": map[string]any{"name": c.Name, "arguments": c.Arguments},
			}}})
		}
		if f.completionTokens > 0 {
			fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":%d}}\n\n", f.completionTokens)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeLLM) request(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func toolCall(name string, args map[string]any) FunctionCall {
	b, _ := json.Marshal(args)
	return FunctionCall{Name: name, Arguments: string(b)}
}

// collector records events; safe for use as a Run sink.
type collector struct {
	mu     sync.Mutex
	events []Event
}

func (c *collector) sink(ev Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collector) types() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, ev := range c.events {
		if ev.Type != EvDelta && ev.Type != EvReasoning && ev.Type != EvToolStart && ev.Type != EvChanges {
			out = append(out, ev.Type)
		}
	}
	return out
}

func (c *collector) find(typ string) []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Event
	for _, ev := range c.events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func noApprover(t *testing.T) Approver {
	return func(context.Context, ToolCall, string) (bool, error) {
		t.Error("approver called unexpectedly")
		return false, nil
	}
}

func newTestSession(t *testing.T, url string) (*Session, string) {
	root := t.TempDir()
	return NewSession(url, "test-model", root, filepath.Join(t.TempDir(), "s.json"), nil), root
}

func TestRunTextOnly(t *testing.T) {
	_, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "hello there friend"} })
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "hi", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.types(), ","); got != "user,assistant,done" {
		t.Fatalf("events = %s", got)
	}
	var streamed string
	for _, ev := range c.find(EvDelta) {
		streamed += ev.Text
	}
	if streamed != "hello there friend" || c.find(EvAssistant)[0].Text != "hello there friend" {
		t.Fatalf("streamed %q, assistant %q", streamed, c.find(EvAssistant)[0].Text)
	}
	saved, err := loadSession(s.Path())
	if err != nil || len(saved) != 3 || saved[2].Model != "test-model" {
		t.Fatalf("saved session = %+v, %v", saved, err)
	}
	if s.Running() {
		t.Fatal("still running after Run returned")
	}
}

func TestRunApprovalAcceptedAndDenied(t *testing.T) {
	root := t.TempDir()
	yes, no := filepath.Join(root, "yes"), filepath.Join(root, "no")
	_, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: []FunctionCall{
				toolCall("bash", map[string]any{"command": "touch " + yes}),
				toolCall("bash", map[string]any{"command": "touch " + no}),
			}}
		}
		return fakeReply{text: "done"}
	})
	s := NewSession(url, "m", root, "", nil)
	var asked []string
	approve := func(_ context.Context, tc ToolCall, _ string) (bool, error) {
		asked = append(asked, tc.Function.Arguments)
		return len(asked) == 1, nil // approve the first, deny the second
	}
	var c collector
	if err := s.Run(context.Background(), "make files", nil, c.sink, approve); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %d times", len(asked))
	}
	if _, err := os.Stat(yes); err != nil {
		t.Error("approved command did not run")
	}
	if _, err := os.Stat(no); err == nil {
		t.Error("denied command ran")
	}
	if got := strings.Join(c.types(), ","); got != "user,assistant,tool_call,approval,tool_result,tool_call,approval,assistant,done" {
		t.Fatalf("events = %s", got)
	}
	msgs := s.Messages()
	if last := msgs[len(msgs)-2]; last.Role != "tool" || *last.Content != "The user denied this tool call." {
		t.Fatalf("denied tool message = %+v", last)
	}
}

func TestRunAutoModeSkipsApproval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f")
	_, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: []FunctionCall{toolCall("bash", map[string]any{"command": "touch " + target})}}
		}
		return fakeReply{text: "ok"}
	})
	s := NewSession(url, "m", root, "", nil)
	s.SetSettings(Settings{Auto: true})
	var c collector
	if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("command did not run in auto mode")
	}
	if a := c.find(EvToolCall)[0].Action; a != "allow" {
		t.Fatalf("action = %s", a)
	}
}

func TestRunPlanModeBlocksWrites(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.txt")
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: []FunctionCall{toolCall("write_file", map[string]any{"path": target, "content": "x"})}}
		}
		return fakeReply{text: "ok"}
	})
	s := NewSession(url, "m", root, "", nil)
	s.SetSettings(Settings{Plan: true})
	var c collector
	if err := s.Run(context.Background(), "write it", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("file written in plan mode")
	}
	if ev := c.find(EvToolCall)[0]; ev.Action != "deny" || !strings.Contains(ev.Reason, "Plan mode") {
		t.Fatalf("tool_call = %+v", ev)
	}
	for _, tool := range llm.request(0)["tools"].([]any) {
		if name := tool.(map[string]any)["function"].(map[string]any)["name"]; name == "write_file" {
			t.Fatal("write_file offered in plan mode")
		}
	}
}

func TestRunWriteDiff(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.txt")
	os.WriteFile(target, []byte("a\nb\n"), 0o644)
	_, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: []FunctionCall{toolCall("write_file", map[string]any{"path": target, "content": "a\nc\n"})}}
		}
		return fakeReply{text: "ok"}
	})
	s := NewSession(url, "m", root, "", nil)
	var c collector
	if err := s.Run(context.Background(), "edit", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	want := []DiffLine{{" ", "a"}, {"-", "b"}, {"+", "c"}}
	got := c.find(EvToolCall)[0].Diff
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("diff = %v, want %v", got, want)
	}
	if data, _ := os.ReadFile(target); string(data) != "a\nc\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestRunToolRoundLimit(t *testing.T) {
	llm, url := newFakeLLM(t, func(_ int, req map[string]any) fakeReply {
		if _, hasTools := req["tools"]; !hasTools {
			return fakeReply{text: "final answer"}
		}
		return fakeReply{calls: []FunctionCall{toolCall("bash", map[string]any{"command": "pwd"})}}
	})
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "loop forever", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if n, max := len(llm.requests), s.Settings().MaxRounds; n != max+2 {
		t.Fatalf("made %d requests, want %d", n, max+2)
	}
	if len(c.find(EvNotice)) != 1 {
		t.Fatal("no round-limit notice")
	}
	last := llm.request(len(llm.requests) - 1)["messages"].([]any)
	notice := last[len(last)-1].(map[string]any)
	if notice["role"] != "user" || !strings.Contains(notice["content"].(string), "tool-call limit") {
		t.Fatalf("last message = %v", notice)
	}
}

func TestRunCancelWhileAwaitingApproval(t *testing.T) {
	_, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		return fakeReply{calls: []FunctionCall{
			toolCall("bash", map[string]any{"command": "touch a"}),
			toolCall("bash", map[string]any{"command": "touch b"}),
		}}
	})
	s, _ := newTestSession(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	approve := func(ctx context.Context, _ ToolCall, _ string) (bool, error) {
		cancel()
		<-ctx.Done()
		return false, ctx.Err()
	}
	var c collector
	err := s.Run(ctx, "go", nil, c.sink, approve)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	types := c.types()
	if types[len(types)-2] != EvCancelled || types[len(types)-1] != EvDone {
		t.Fatalf("events = %v", types)
	}
	// Both tool calls still get a tool message, so the history stays valid.
	var toolMsgs int
	for _, m := range s.Messages() {
		if m.Role == "tool" {
			toolMsgs++
		}
	}
	if toolMsgs != 2 {
		t.Fatalf("tool messages = %d, want 2", toolMsgs)
	}
	// And the session can run again afterwards.
	if s.Running() {
		t.Fatal("still marked running")
	}
}

func TestRunBusy(t *testing.T) {
	release := make(chan struct{})
	_, url := newFakeLLM(t, func(int, map[string]any) fakeReply {
		<-release
		return fakeReply{text: "ok"}
	})
	s, _ := newTestSession(t, url)
	done := make(chan error)
	go func() { done <- s.Run(context.Background(), "one", nil, func(Event) {}, noApprover(t)) }()
	deadline := time.Now().Add(2 * time.Second)
	for !s.Running() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Run(context.Background(), "two", nil, func(Event) {}, noApprover(t)); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Run err = %v", err)
	}
	if err := s.SetSettings(Settings{Plan: true}); !errors.Is(err, ErrBusy) {
		t.Fatalf("SetSettings during run err = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSystemPromptFollowsSettings(t *testing.T) {
	s := NewSession("", "m", "/proj", "", nil)
	s.SetSettings(Settings{Plan: true, TDD: true})
	sys := *s.Messages()[0].Content
	if !strings.Contains(sys, "PLANNING MODE") || !strings.Contains(sys, "TEST-DRIVEN") || strings.Contains(sys, "ADHD") {
		t.Fatalf("system prompt missing mode sections")
	}
	if s.Settings().Model != "m" {
		t.Fatal("empty model in settings should keep the current model")
	}
}

func TestUndoRestoresFilesAndConversation(t *testing.T) {
	root := t.TempDir()
	notes := filepath.Join(root, "notes.txt")
	os.WriteFile(notes, []byte("v0\n"), 0o644)
	_, url := newFakeLLM(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if last["role"] == "tool" {
			return fakeReply{text: "done"}
		}
		version := last["content"].(string) // the user says which version to write
		return fakeReply{calls: []FunctionCall{toolCall("write_file", map[string]any{"path": "notes.txt", "content": version + "\n"})}}
	})
	path := filepath.Join(t.TempDir(), "s.json")
	s := NewSession(url, "m", root, path, nil)
	if s.cp == nil {
		t.Fatalf("no checkpointer: %v", s.cpErr)
	}
	for _, v := range []string{"v1", "v2"} {
		if err := s.Run(context.Background(), v, nil, func(Event) {}, noApprover(t)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(notes); string(b) != "v2\n" {
		t.Fatalf("notes = %q", b)
	}

	res, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(notes); string(b) != "v1\n" || !res.FilesRestored || res.Text != "v2" {
		t.Fatalf("after one undo: notes %q, result %+v", b, res)
	}
	if fmt.Sprint(res.Files) != "[notes.txt]" {
		t.Fatalf("files = %v", res.Files)
	}
	if n := len(s.Messages()); n != 5 { // system + v1 turn (user, call, result, answer)
		t.Fatalf("messages after undo = %d", n)
	}

	// Undo points survive a restart.
	reloaded, _ := loadSession(path)
	s2 := NewSession(url, "m", root, path, reloaded)
	if !s2.CanUndo() {
		t.Fatal("undo point lost on reload")
	}
	if _, err := s2.Undo(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(notes); string(b) != "v0\n" {
		t.Fatalf("after second undo notes = %q", b)
	}
	if len(s2.Messages()) != 1 {
		t.Fatalf("messages = %d, want just the system prompt", len(s2.Messages()))
	}
	if _, err := s2.Undo(); err == nil {
		t.Fatal("undo with nothing left succeeded")
	}
}


func TestSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	root := t.TempDir()
	s := NewSession("http://unused", "default-model", root, path, nil)
	if err := s.SetSettings(Settings{Model: "big-model", Plan: true, ADHD: true, MaxRounds: 17}); err != nil {
		t.Fatal(err)
	}
	s2 := NewSession("http://unused", "default-model", root, path, nil)
	st := s2.Settings()
	if st.Model != "big-model" || !st.Plan || !st.ADHD || st.Auto || st.MaxRounds != 17 {
		t.Fatalf("reloaded settings = %+v", st)
	}
	if sys := *s2.Messages()[0].Content; !strings.Contains(sys, "PLANNING MODE") || !strings.Contains(sys, "ADHD") {
		t.Fatal("system prompt doesn't reflect the reloaded settings")
	}
	// Renaming later keeps the settings.
	title := "named"
	s2.SetLabel(&title, nil)
	if st := NewSession("http://unused", "default-model", root, path, nil).Settings(); st.Model != "big-model" {
		t.Fatalf("settings lost after rename: %+v", st)
	}
}

func TestTurnChanges(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "keep.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("bye\n"), 0o644)
	_, url := newFakeLLM(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if last["role"] == "tool" {
			return fakeReply{text: "done"}
		}
		switch last["content"] {
		case "edit":
			return fakeReply{calls: []FunctionCall{
				toolCall("write_file", map[string]any{"path": "keep.txt", "content": "one\nTWO\nthree\n"}),
				toolCall("write_file", map[string]any{"path": "new.txt", "content": "hello\n"}),
				toolCall("bash", map[string]any{"command": "rm old.txt"}),
			}}
		}
		return fakeReply{text: "just talking"}
	})
	s := NewSession(url, "m", root, filepath.Join(t.TempDir(), "s.json"), nil)
	approveAll := func(context.Context, ToolCall, string) (bool, error) { return true, nil }
	var c collector
	s.Run(context.Background(), "edit", nil, c.sink, approveAll)
	s.Run(context.Background(), "chat", nil, c.sink, approveAll)

	ends := c.find(EvChanges)
	if len(ends) != 2 || ends[0].Turn != 1 || ends[0].Files != 2 || ends[1].Turn != 2 || ends[1].Files != 0 {
		t.Fatalf("changes events = %+v", ends)
	}
	// rm is denied by policy, so old.txt survives: 2 files really changed.
	files, err := s.Changes(1)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	if k := byPath["keep.txt"]; k.Status != "modified" || k.Added != 1 || k.Removed != 1 {
		t.Fatalf("keep.txt = %+v", k)
	}
	if n := byPath["new.txt"]; n.Status != "added" || n.Added != 1 || n.Removed != 0 {
		t.Fatalf("new.txt = %+v", n)
	}
	var ops string
	for _, l := range byPath["keep.txt"].Diff {
		ops += l.Op
	}
	if ops != "@ -+ " && ops != "@  -+ " && !strings.Contains(ops, "-+") {
		t.Fatalf("keep.txt diff ops = %q (%+v)", ops, byPath["keep.txt"].Diff)
	}
	if files, err := s.Changes(2); err != nil || len(files) != 0 {
		t.Fatalf("turn 2 changes = %+v, %v", files, err)
	}
	if _, err := s.Changes(3); err == nil {
		t.Fatal("changes for a turn that doesn't exist")
	}
	// The counts survive a reload.
	s2 := NewSession(url, "m", root, s.Path(), s.Messages())
	if turns := s2.Turns(); len(turns) != 2 || turns[0].Files != 2 {
		t.Fatalf("reloaded turns = %+v", turns)
	}
}

func TestRunRejectsImagesForNonVisionModel(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"text-only","capabilities":{"vision":false}}]}`)
			return
		}
		callCount++
		t.Error("model server should not have been called for an unsupported model")
	}))
	t.Cleanup(srv.Close)

	s := NewSession(srv.URL, "text-only", t.TempDir(), filepath.Join(t.TempDir(), "s.json"), nil)
	var c collector
	err := s.Run(context.Background(), "what's in this?", []string{"data:image/png;base64,AA=="}, c.sink, noApprover(t))
	if err == nil || !strings.Contains(err.Error(), "doesn't accept images") {
		t.Fatalf("err = %v", err)
	}
	if callCount != 0 {
		t.Fatal("model server was called despite lacking vision")
	}
	users := c.find(EvUser)
	if len(users) != 1 || len(users[0].Images) != 1 {
		t.Fatalf("EvUser = %+v", users)
	}
	errs := c.find(EvError)
	if len(errs) != 1 || !strings.Contains(errs[0].Text, "text-only") {
		t.Fatalf("EvError = %+v", errs)
	}
}

func TestRunSendsImagesToVisionModel(t *testing.T) {
	var gotContent any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"vision-model","capabilities":{"vision":true}}]}`)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		msgs := req["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		gotContent = last["content"]
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"I see it\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	s := NewSession(srv.URL, "vision-model", t.TempDir(), filepath.Join(t.TempDir(), "s.json"), nil)
	var c collector
	dataURL := "data:image/png;base64,AA=="
	err := s.Run(context.Background(), "what's in this?", []string{dataURL}, c.sink, noApprover(t))
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := gotContent.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v", gotContent)
	}
	textPart := parts[0].(map[string]any)
	if textPart["type"] != "text" || textPart["text"] != "what's in this?" {
		t.Fatalf("text part = %#v", textPart)
	}
	imgPart := parts[1].(map[string]any)
	imgURL := imgPart["image_url"].(map[string]any)
	if imgPart["type"] != "image_url" || imgURL["url"] != dataURL {
		t.Fatalf("image part = %#v", imgPart)
	}
}

func TestBackfillOlderTurns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "s.json")
	cp, _ := newCheckpointer(root)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("1\n"), 0o644)
	c1, _ := cp.snapshot("before 1")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("2\n"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("new\n"), 0o644)
	c2, _ := cp.snapshot("before 2")
	msgs := []Message{msg("system", "s"), msg("user", "one"), msg("assistant", "ok"), msg("user", "two"), msg("assistant", "ok")}
	saveSession(path, msgs)
	// Turns as an older goharness recorded them: no After, no Files.
	saveMeta(path, sessionMeta{ProjectRoot: root, Turns: []turnMark{{MsgIndex: 1, Checkpoint: c1, Text: "one"}, {MsgIndex: 3, Checkpoint: c2, Text: "two"}}})

	s := NewSession("http://unused", "m", root, path, msgs)
	var turns []turnMark
	for i := 0; i < 100; i++ {
		if turns = s.Turns(); turns[0].Files > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if turns[0].Files != 2 || turns[0].After != c2 || turns[1].After != "" {
		t.Fatalf("turns after backfill = %+v", turns)
	}
	if files, err := s.Changes(1); err != nil || len(files) != 2 {
		t.Fatalf("changes = %+v, %v", files, err)
	}
	if loadMeta(path).Turns[0].Files != 2 {
		t.Fatal("backfill not saved")
	}
}

func TestSystemPromptIncludesICMWhenEnabled(t *testing.T) {
	off := systemPrompt("/proj", Settings{Model: "m"})
	if strings.Contains(off, "STAGED PROJECT") {
		t.Fatal("ICM text present without ICM enabled")
	}
	on := systemPrompt("/proj", Settings{Model: "m", ICM: true})
	if !strings.Contains(on, "STAGED PROJECT") || !strings.Contains(on, "stages/NN_name") {
		t.Fatalf("ICM text missing when enabled:\n%s", on)
	}
}

func TestModeLabelShowsICM(t *testing.T) {
	if got := modeLabel(Settings{ICM: true}); !strings.Contains(got, "+ICM") {
		t.Fatalf("modeLabel = %q, want +ICM", got)
	}
	if got := modeLabel(Settings{}); strings.Contains(got, "+ICM") {
		t.Fatalf("modeLabel = %q, ICM badge present when off", got)
	}
}
