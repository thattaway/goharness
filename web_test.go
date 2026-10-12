package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const testToken = "secret-token"

// testLLMURLs remembers each test server's fake model, for tests that
// rebuild the handler with different options.
var testLLMURLs = map[*httptest.Server]string{}

func llmURLOf(t *testing.T, srv *httptest.Server) string { return testLLMURLs[srv] }

// newTestWeb starts the web server against a fake model, with HOME pointed
// at a temp dir so sessions and tokens stay out of the real one.
func newTestWeb(t *testing.T, next func(n int, req map[string]any) fakeReply) (*httptest.Server, string) {
	t.Setenv("HOME", t.TempDir())
	_, llmURL := newFakeLLM(t, next)
	root := t.TempDir()
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "app") })
	srv := httptest.NewServer(newWebServer(llmURL, "test-model", root, testToken, static).routes())
	t.Cleanup(srv.Close)
	testLLMURLs[srv] = llmURL
	return srv, root
}

func apiReq(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, srv.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// sseReader reads events from an open event stream.
type sseReader struct {
	resp *http.Response
	sc   *bufio.Scanner
}

func openEvents(t *testing.T, srv *httptest.Server, id string, since int64, lastEventID string) *sseReader {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/sessions/%s/events?since=%d", srv.URL, id, since), nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &sseReader{resp: resp, sc: bufio.NewScanner(resp.Body)}
}

// next returns the next event, failing the test if none arrives in time.
func (s *sseReader) next(t *testing.T) Event {
	t.Helper()
	got := make(chan Event, 1)
	go func() {
		for s.sc.Scan() {
			if data, ok := strings.CutPrefix(s.sc.Text(), "data: "); ok {
				var ev Event
				json.Unmarshal([]byte(data), &ev)
				got <- ev
				return
			}
		}
		close(got)
	}()
	select {
	case ev, ok := <-got:
		if !ok {
			t.Fatal("event stream ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return Event{}
}

// until reads events up to and including the first one of type typ.
func (s *sseReader) until(t *testing.T, typ string) []Event {
	t.Helper()
	var evs []Event
	for {
		ev := s.next(t)
		evs = append(evs, ev)
		if ev.Type == typ {
			return evs
		}
	}
}

// withoutChanges drops the end-of-turn "changes" event, for tests about
// what came before it.
func withoutChanges(evs []Event) []Event {
	var out []Event
	for _, ev := range evs {
		if ev.Type != EvChanges {
			out = append(out, ev)
		}
	}
	return out
}

func createSession(t *testing.T, srv *httptest.Server) sessionSnapshot {
	resp := apiReq(t, srv, "POST", "/api/sessions", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: %d", resp.StatusCode)
	}
	return decode[sessionSnapshot](t, resp)
}

func TestWebAuth(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })

	for _, auth := range []string{"", "Bearer wrong"} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/sessions", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d, want 401", auth, resp.StatusCode)
		}
	}

	// ?token= swaps the token for a cookie and strips it from the URL.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := noRedirect.Get(srv.URL + "/?token=" + testToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == tokenCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", cookie)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/sessions", nil)
	req.AddCookie(cookie)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie auth: %d", resp.StatusCode)
	}

	// A wrong token in the URL sets no cookie.
	resp, _ = noRedirect.Get(srv.URL + "/?token=nope")
	resp.Body.Close()
	if len(resp.Cookies()) != 0 {
		t.Fatal("cookie set for a wrong token")
	}

	// POST /api/login.
	resp, _ = http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"token":"`+testToken+`"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(resp.Cookies()) == 0 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
}

func TestWebRejectsCrossOrigin(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	req, _ := http.NewRequest("POST", srv.URL+"/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
	req.Header.Set("Origin", srv.URL)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same-origin status %d, want 201", resp.StatusCode)
	}
}

func TestWebRunApproveAndReplay(t *testing.T) {
	var target string
	release := make(chan struct{})
	srv, root := newTestWeb(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{text: "working ", calls: []FunctionCall{toolCall("bash", map[string]any{"command": "touch " + target})}}
		default:
			<-release // hold the run open so we can test busy + replay
			return fakeReply{text: "all done"}
		}
	})
	target = filepath.Join(root, "made")

	snap := createSession(t, srv)
	events := openEvents(t, srv, snap.ID, snap.Since, "")

	if resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/messages", map[string]string{"text": "make it"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	evs := events.until(t, EvToolCall)
	if evs[0].Type != EvUser || evs[0].Text != "make it" {
		t.Fatalf("first event = %+v", evs[0])
	}
	call := evs[len(evs)-1]
	if call.Action != "ask" {
		t.Fatalf("action = %s", call.Action)
	}

	// Approve (the approver may register a moment after the event is sent).
	var status int
	for i := 0; i < 50; i++ {
		resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/approvals/"+call.ID, map[string]bool{"approve": true})
		resp.Body.Close()
		if status = resp.StatusCode; status != http.StatusNotFound {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != http.StatusNoContent {
		t.Fatalf("approve: %d", status)
	}
	evs = events.until(t, EvToolResult)
	if _, err := os.Stat(target); err != nil {
		t.Fatal("approved command did not run")
	}

	// The run is now blocked on the second model call: a second message is refused.
	if resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/messages", map[string]string{"text": "again"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("send while running: %d, want 409", resp.StatusCode)
	}
	// So are settings changes.
	if resp := apiReq(t, srv, "PUT", "/api/sessions/"+snap.ID+"/settings", Settings{Plan: true}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("settings while running: %d, want 409", resp.StatusCode)
	}

	// A reloaded page gets the pre-run conversation plus a replay of the run.
	mid := decode[sessionSnapshot](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID, nil))
	if !mid.Running || len(mid.Messages) != 0 {
		t.Fatalf("mid-run snapshot: running=%v messages=%d", mid.Running, len(mid.Messages))
	}
	replay := openEvents(t, srv, snap.ID, mid.Since, "")
	if ev := replay.next(t); ev.Type != EvUser {
		t.Fatalf("replay starts with %s", ev.Type)
	}

	// A reconnect with Last-Event-ID resumes right after that event.
	resumed := openEvents(t, srv, snap.ID, 0, fmt.Sprint(call.Seq))
	if ev := resumed.next(t); ev.Seq != call.Seq+1 {
		t.Fatalf("resumed at seq %d, want %d", ev.Seq, call.Seq+1)
	}

	close(release)
	evs = withoutChanges(events.until(t, EvDone))
	if a := evs[len(evs)-2]; a.Type != EvAssistant || a.Text != "all done" {
		t.Fatalf("final assistant = %+v", a)
	}

	// Afterwards the snapshot has the whole conversation, minus the system prompt.
	var final sessionSnapshot
	for i := 0; i < 50; i++ {
		if final = decode[sessionSnapshot](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID, nil)); !final.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	roles := []string{}
	for _, m := range final.Messages {
		roles = append(roles, m.Role)
	}
	if got := strings.Join(roles, ","); got != "user,assistant,tool,assistant" {
		t.Fatalf("roles = %s", got)
	}

	// Subscribing from a seq the server doesn't have yet gets resync.
	stale := openEvents(t, srv, snap.ID, final.Since+100, "")
	if ev := stale.next(t); ev.Type != "resync" {
		t.Fatalf("future subscribe got %s, want resync", ev.Type)
	}

	// The session shows up in the list with its title.
	list := decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil))
	if len(list) != 1 || list[0].ID != snap.ID || list[0].Title != "make it" {
		t.Fatalf("list = %+v", list)
	}
}

func TestWebDenyAndCancel(t *testing.T) {
	srv, _ := newTestWeb(t, func(n int, _ map[string]any) fakeReply {
		return fakeReply{calls: []FunctionCall{toolCall("bash", map[string]any{"command": "touch x"})}}
	})
	snap := createSession(t, srv)
	events := openEvents(t, srv, snap.ID, snap.Since, "")
	apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/messages", map[string]string{"text": "go"}).Body.Close()

	call := events.until(t, EvToolCall)
	time.Sleep(50 * time.Millisecond)
	apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/approvals/"+call[len(call)-1].ID, map[string]bool{"approve": false}).Body.Close()
	if ev := events.until(t, EvApproval); ev[len(ev)-1].Approved {
		t.Fatal("approval event says approved")
	}

	events.until(t, EvToolCall) // the model asks again
	if resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/cancel", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}
	evs := withoutChanges(events.until(t, EvDone))
	if evs[len(evs)-2].Type != EvCancelled {
		t.Fatalf("events before done: %+v", evs)
	}
}

// TestWebEventsSurviveRestart checks that a run's events are readable from
// a brand-new server process (same HOME, so the same on-disk session), and
// that a run cut off mid-flight - the goharness process died before EvDone -
// is reported as interrupted instead of looking silently stuck forever.
func TestWebEventsSurviveRestart(t *testing.T) {
	release := make(chan struct{})
	srv, root := newTestWeb(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{text: "working", calls: []FunctionCall{toolCall("bash", map[string]any{"command": "true"})}}
		default:
			<-release // never released: this simulates the process dying here
			return fakeReply{text: "done"}
		}
	})

	snap := createSession(t, srv)
	events := openEvents(t, srv, snap.ID, snap.Since, "")
	apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/messages", map[string]string{"text": "go"}).Body.Close()

	call := events.until(t, EvToolCall)
	toolCallEv := call[len(call)-1]
	var status int
	for i := 0; i < 50; i++ {
		resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/approvals/"+toolCallEv.ID, map[string]bool{"approve": true})
		resp.Body.Close()
		if status = resp.StatusCode; status != http.StatusNotFound {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != http.StatusNoContent {
		t.Fatalf("approve: %d", status)
	}
	events.until(t, EvToolResult) // the run is now blocked on the second model call

	// Simulate a restart: a brand-new server sharing the same HOME/root, so
	// it has to load this session's events from disk instead of memory -
	// nothing here shares any in-process state with srv.
	restarted := httptest.NewServer(newWebServer(llmURLOf(t, srv), "test-model", root, testToken, http.NotFoundHandler()).routes())
	t.Cleanup(restarted.Close)

	after := openEvents(t, restarted, snap.ID, 0, "")
	var evs []Event
	sawInterrupted := false
	for i := 0; i < 20; i++ {
		ev := after.next(t)
		evs = append(evs, ev)
		if ev.Type == EvNotice && strings.Contains(ev.Text, "interrupted") {
			sawInterrupted = true
			break
		}
		if ev.Type == EvDone {
			break
		}
	}
	if !sawInterrupted {
		t.Fatalf("events after restart = %+v, want an interrupted notice, not a silent stop", evs)
	}
	if evs[0].Type != EvUser {
		t.Fatalf("first replayed event = %+v, want the original user message", evs[0])
	}

	mid := decode[sessionSnapshot](t, apiReq(t, restarted, "GET", "/api/sessions/"+snap.ID, nil))
	if mid.Running {
		t.Fatal("restarted server has no live goroutine for this session; it must not report running=true")
	}

	close(release) // let the orphaned original goroutine finish so it doesn't leak past the test
}

func TestWebSettingsAndUnknownSession(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	st := decode[Settings](t, apiReq(t, srv, "PUT", "/api/sessions/"+snap.ID+"/settings", Settings{Plan: true, Auto: true}))
	if !st.Plan || !st.Auto || st.Model != "test-model" {
		t.Fatalf("settings = %+v", st)
	}
	for _, id := range []string{"nope", "..%2F..%2Fetc%2Fpasswd", ".hidden"} {
		if resp := apiReq(t, srv, "GET", "/api/sessions/"+id, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET session %q: %d, want 404", id, resp.StatusCode)
		}
	}
}

func TestWebProjectsUndoAndPermissions(t *testing.T) {
	t.Cleanup(func() { userRules.Lock(); userRules.rules = nil; userRules.Unlock() })
	srv, root := newTestWeb(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		if msgs[len(msgs)-1].(map[string]any)["role"] == "tool" {
			return fakeReply{text: "ok"}
		}
		return fakeReply{calls: []FunctionCall{toolCall("write_file", map[string]any{"path": "made.txt", "content": "x"})}}
	})
	_ = root

	// A projects root makes its subfolders selectable; symlinks and other folders are not.
	parent := t.TempDir()
	os.Mkdir(filepath.Join(parent, "alpha"), 0o755)
	os.Mkdir(filepath.Join(parent, ".hidden"), 0o755)
	os.Symlink("/", filepath.Join(parent, "sneaky"))
	srv.Config.Handler = func() http.Handler {
		ws := newWebServer(llmURLOf(t, srv), "test-model", root, testToken, http.NotFoundHandler())
		ws.projectRoots = []string{parent}
		return ws.routes()
	}()
	projects := decode[[]project](t, apiReq(t, srv, "GET", "/api/projects", nil))
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != filepath.Base(root)+",alpha" {
		t.Fatalf("projects = %v", names)
	}
	if resp := apiReq(t, srv, "POST", "/api/sessions", map[string]string{"project": "/etc"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("disallowed project: %d", resp.StatusCode)
	}
	alpha := filepath.Join(parent, "alpha")
	resp := apiReq(t, srv, "POST", "/api/sessions", map[string]string{"project": alpha})
	snap := decode[sessionSnapshot](t, resp)
	if snap.ProjectRoot != alpha || snap.CanUndo {
		t.Fatalf("snapshot = %+v", snap)
	}

	// Run a turn in alpha: the file lands there, then undo removes it.
	events := openEvents(t, srv, snap.ID, snap.Since, "")
	apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/messages", map[string]string{"text": "make a file"}).Body.Close()
	events.until(t, EvDone)
	if _, err := os.Stat(filepath.Join(alpha, "made.txt")); err != nil {
		t.Fatal("file not written in the session's project")
	}
	var after sessionSnapshot
	for i := 0; i < 50; i++ {
		if after = decode[sessionSnapshot](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID, nil)); after.CanUndo {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !after.CanUndo {
		t.Fatal("can't undo after a turn")
	}
	res := decode[UndoResult](t, apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/undo", nil))
	if res.Text != "make a file" || !res.FilesRestored {
		t.Fatalf("undo = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(alpha, "made.txt")); err == nil {
		t.Fatal("undo left the created file")
	}
	if ev := events.next(t); ev.Type != evReset {
		t.Fatalf("after undo got %s, want reset", ev.Type)
	}
	if resp := apiReq(t, srv, "POST", "/api/sessions/"+snap.ID+"/undo", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second undo: %d", resp.StatusCode)
	}

	// Permission rules round-trip and apply.
	perms := decode[map[string]any](t, apiReq(t, srv, "GET", "/api/permissions", nil))
	if len(perms["defaults"].([]any)) == 0 || len(perms["rules"].([]any)) != 0 {
		t.Fatalf("permissions = %v", perms)
	}
	if resp := apiReq(t, srv, "PUT", "/api/permissions", map[string]any{"rules": []Rule{{Pattern: "go test*", Action: "allow"}}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("save rules: %d", resp.StatusCode)
	}
	got := decode[map[string]string](t, apiReq(t, srv, "POST", "/api/permissions/check", map[string]string{"command": "go test ./..."}))
	if got["action"] != "allow" {
		t.Fatalf("check = %v", got)
	}
	if resp := apiReq(t, srv, "PUT", "/api/permissions", map[string]any{"rules": []Rule{{Pattern: "x", Action: "sure"}}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad rule: %d", resp.StatusCode)
	}
}

func TestWebChooseAnyDirectory(t *testing.T) {
	srv, root := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	parent := t.TempDir()
	deep := filepath.Join(parent, "a", "b")
	os.MkdirAll(deep, 0o755)
	os.Mkdir(filepath.Join(parent, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(parent, "file.txt"), []byte("x"), 0o644)
	home, _ := os.UserHomeDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)

	build := func(anyDir bool, roots ...string) {
		ws := newWebServer(llmURLOf(t, srv), "test-model", root, testToken, http.NotFoundHandler())
		ws.projectRoots = roots
		ws.allowAnyDir = anyDir
		srv.Config.Handler = ws.routes()
	}
	create := func(dir string) int {
		resp := apiReq(t, srv, "POST", "/api/sessions", map[string]string{"project": dir})
		resp.Body.Close()
		return resp.StatusCode
	}

	// Restricted mode: nested folders under a projects root are fine, others aren't.
	build(false, parent)
	if code := create(deep); code != http.StatusCreated {
		t.Fatalf("nested folder under root: %d", code)
	}
	if code := create(t.TempDir()); code != http.StatusBadRequest {
		t.Fatalf("folder outside roots: %d", code)
	}
	if resp := apiReq(t, srv, "GET", "/api/dirs?path=/etc", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browsing outside roots: %d", resp.StatusCode)
	}

	// Any-dir mode: any existing folder, but not secret ones or files.
	build(true)
	elsewhere := t.TempDir()
	if code := create(elsewhere); code != http.StatusCreated {
		t.Fatalf("any folder: %d", code)
	}
	for _, bad := range []string{filepath.Join(home, ".ssh"), filepath.Join(parent, "file.txt"), filepath.Join(parent, "missing"), "relative/path"} {
		if code := create(bad); code != http.StatusBadRequest {
			t.Errorf("create in %s: %d, want 400", bad, code)
		}
	}

	// Browsing lists subfolders only, hidden ones last, with a parent link.
	type listing struct {
		Path    string    `json:"path"`
		Parent  string    `json:"parent"`
		Entries []project `json:"entries"`
		Allowed bool      `json:"allowed"`
	}
	l := decode[listing](t, apiReq(t, srv, "GET", "/api/dirs?path="+parent, nil))
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "a,.hidden" || l.Parent != filepath.Dir(parent) || !l.Allowed {
		t.Fatalf("listing = %+v", l)
	}
	if def := decode[listing](t, apiReq(t, srv, "GET", "/api/dirs", nil)); def.Path != root {
		t.Fatalf("default browse path = %s, want the default project", def.Path)
	}
	if resp := apiReq(t, srv, "GET", "/api/dirs?path="+filepath.Join(parent, "nope"), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing dir: %d", resp.StatusCode)
	}
}

// runTurn sends a message and waits for the run to finish.
func runTurn(t *testing.T, srv *httptest.Server, id, text string) {
	t.Helper()
	events := openEvents(t, srv, id, 0, "")
	apiReq(t, srv, "POST", "/api/sessions/"+id+"/messages", map[string]string{"text": text}).Body.Close()
	events.until(t, EvDone)
	for i := 0; i < 50 && decode[sessionSnapshot](t, apiReq(t, srv, "GET", "/api/sessions/"+id, nil)).Running; i++ {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebRenameAndPin(t *testing.T) {
	srv, root := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	a, b := createSession(t, srv), createSession(t, srv)
	runTurn(t, srv, a.ID, "first question")
	runTurn(t, srv, b.ID, "second question")

	resp := apiReq(t, srv, "PATCH", "/api/sessions/"+a.ID, map[string]any{"title": "  My important chat  ", "pinned": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d", resp.StatusCode)
	}
	resp.Body.Close()
	list := decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil))
	if list[0].ID != a.ID || !list[0].Pinned || list[0].Title != "My important chat" {
		t.Fatalf("pinned session not first or not renamed: %+v", list)
	}

	// Metadata survives a server restart (a fresh registry reading from disk),
	// and a later turn doesn't wipe it.
	srv.Config.Handler = newWebServer(llmURLOf(t, srv), "test-model", root, testToken, http.NotFoundHandler()).routes()
	runTurn(t, srv, a.ID, "another question")
	list = decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil))
	if list[0].ID != a.ID || list[0].Title != "My important chat" || !list[0].Pinned {
		t.Fatalf("after restart and a turn: %+v", list[0])
	}

	// An empty title falls back to the first message; pinned can change alone.
	apiReq(t, srv, "PATCH", "/api/sessions/"+a.ID, map[string]any{"title": ""}).Body.Close()
	apiReq(t, srv, "PATCH", "/api/sessions/"+a.ID, map[string]any{"pinned": false}).Body.Close()
	for _, s := range decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil)) {
		if s.ID == a.ID && (s.Pinned || s.Title != "first question") {
			t.Fatalf("after reset: %+v", s)
		}
	}
	if resp := apiReq(t, srv, "PATCH", "/api/sessions/"+a.ID, map[string]any{"title": strings.Repeat("x", 300)}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("overlong title: %d", resp.StatusCode)
	}
}

func TestWebDeleteSessions(t *testing.T) {
	release := make(chan struct{})
	srv, _ := newTestWeb(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		if strings.Contains(fmt.Sprint(msgs[len(msgs)-1]), "slow") {
			<-release
		}
		return fakeReply{text: "ok"}
	})
	a, b, c, fresh := createSession(t, srv), createSession(t, srv), createSession(t, srv), createSession(t, srv)
	runTurn(t, srv, a.ID, "one")
	runTurn(t, srv, b.ID, "two")
	events := openEvents(t, srv, c.ID, 0, "")
	apiReq(t, srv, "POST", "/api/sessions/"+c.ID+"/messages", map[string]string{"text": "slow one"}).Body.Close()
	events.until(t, EvUser)
	apiReq(t, srv, "PATCH", "/api/sessions/"+a.ID, map[string]any{"pinned": true}).Body.Close()

	type result struct {
		Deleted []string          `json:"deleted"`
		Skipped map[string]string `json:"skipped"`
	}
	res := decode[result](t, apiReq(t, srv, "POST", "/api/sessions/delete", map[string]any{
		"ids": []string{a.ID, c.ID, fresh.ID, "no-such-session", "../../etc/passwd"},
	}))
	sort.Strings(res.Deleted)
	want := []string{a.ID, fresh.ID}
	sort.Strings(want)
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("deleted = %v, want %v", res.Deleted, want)
	}
	if !strings.Contains(res.Skipped[c.ID], "running") || res.Skipped["no-such-session"] == "" || res.Skipped["../../etc/passwd"] == "" {
		t.Fatalf("skipped = %v", res.Skipped)
	}

	list := decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil))
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	remaining := []string{b.ID, c.ID}
	sort.Strings(remaining)
	if fmt.Sprint(ids) != fmt.Sprint(remaining) {
		t.Fatalf("remaining = %v, want %v", ids, remaining)
	}
	if resp := apiReq(t, srv, "GET", "/api/sessions/"+a.ID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted session still loads: %d", resp.StatusCode)
	}

	// Files went to the trash, not away.
	home, _ := os.UserHomeDir()
	trashed, _ := filepath.Glob(filepath.Join(home, ".goharness", "trash", "*", a.ID+".*"))
	if len(trashed) != 2 { // .json and .meta
		t.Fatalf("trash has %v", trashed)
	}
	close(release)
}

func TestWebSearchSessions(t *testing.T) {
	srv, _ := newTestWeb(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		q := fmt.Sprint(msgs[len(msgs)-1].(map[string]any)["content"])
		return fakeReply{text: "Here is some advice about " + strings.ToUpper(q[len(q)-5:]) + " and other things."}
	})
	a, b, c := createSession(t, srv), createSession(t, srv), createSession(t, srv)
	runTurn(t, srv, a.ID, "help me set up postgres")
	runTurn(t, srv, b.ID, "write a haiku about rust")
	apiReq(t, srv, "PATCH", "/api/sessions/"+c.ID, map[string]any{"title": "Postgres tuning notes"}).Body.Close()

	search := func(q string) []sessionSummary {
		return decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions?q="+url.QueryEscape(q), nil))
	}
	ids := func(list []sessionSummary) string {
		var out []string
		for _, s := range list {
			out = append(out, s.ID)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	want := []string{a.ID, c.ID}
	sort.Strings(want)
	if got := ids(search("POSTGRES")); got != strings.Join(want, ",") {
		t.Fatalf("postgres matched %s", got)
	}
	// A match inside the conversation (the model's reply) comes with a snippet.
	res := search("advice about")
	if len(res) != 2 {
		t.Fatalf("advice matched %+v", res)
	}
	for _, r := range res {
		if !strings.Contains(strings.ToLower(r.Match), "advice about") {
			t.Fatalf("snippet %q", r.Match)
		}
	}
	if got := search("nothing like this"); len(got) != 0 {
		t.Fatalf("unexpected matches %+v", got)
	}
	if got := search(""); len(got) != 3 {
		t.Fatalf("empty query returned %d", len(got))
	}
}

func TestWebTurnChanges(t *testing.T) {
	srv, root := newTestWeb(t, func(_ int, req map[string]any) fakeReply {
		msgs := req["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if last["role"] == "tool" || last["content"] != "edit" {
			return fakeReply{text: "ok"}
		}
		return fakeReply{calls: []FunctionCall{toolCall("write_file", map[string]any{"path": "a.txt", "content": "new\n"})}}
	})
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644)
	snap := createSession(t, srv)
	runTurn(t, srv, snap.ID, "edit")
	runTurn(t, srv, snap.ID, "chat")

	got := decode[sessionSnapshot](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID, nil))
	if len(got.Turns) != 2 {
		t.Fatalf("turns = %+v", got.Turns)
	}
	t1, t2 := got.Turns[0], got.Turns[1]
	if t1.Turn != 1 || t1.Files != 1 || !t1.HasHistory || got.Messages[t1.MsgIndex].Role != "user" || *got.Messages[t1.MsgIndex].Content != "edit" {
		t.Fatalf("turn 1 = %+v", t1)
	}
	if t2.Files != 0 || *got.Messages[t2.MsgIndex].Content != "chat" {
		t.Fatalf("turn 2 = %+v", t2)
	}

	type changes struct {
		Files []FileChange `json:"files"`
	}
	ch := decode[changes](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID+"/turns/1/changes", nil))
	if len(ch.Files) != 1 || ch.Files[0].Path != "a.txt" || ch.Files[0].Added != 1 || ch.Files[0].Removed != 1 {
		t.Fatalf("changes = %+v", ch.Files)
	}
	for _, bad := range []string{"9", "zero"} {
		if resp := apiReq(t, srv, "GET", "/api/sessions/"+snap.ID+"/turns/"+bad+"/changes", nil); resp.StatusCode < 400 {
			t.Errorf("turn %s: %d", bad, resp.StatusCode)
		}
	}
}

func TestWebSkills(t *testing.T) {
	srv, root := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "book-to-skill", bookSkill, nil)
	snap := createSession(t, srv)
	skills := decode[[]Skill](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID+"/skills", nil))
	if len(skills) != 1 || skills[0].Name != "book-to-skill" || !strings.HasPrefix(skills[0].Description, "Turn any technical book") {
		t.Fatalf("skills = %+v", skills)
	}
}

func TestWebSettingsKeepSubagentModel(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	url := "/api/sessions/" + snap.ID + "/settings"
	st := decode[Settings](t, apiReq(t, srv, "PUT", url, map[string]any{"subagentModel": "small-model"}))
	if st.SubagentModel != "small-model" {
		t.Fatalf("subagent model not set: %+v", st)
	}
	// A client that doesn't know about the field (it omits it) must not wipe it.
	st = decode[Settings](t, apiReq(t, srv, "PUT", url, Settings{Plan: true}))
	if !st.Plan || st.SubagentModel != "small-model" {
		t.Fatalf("subagent model lost by an unrelated settings change: %+v", st)
	}
}

func TestWebStatsContextBudgetFollowsTheModelsWindow(t *testing.T) {
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	url := llmURLOf(t, srv)
	contextLengths.Lock()
	contextLengths.byURL[url] = map[string]int{"test-model": 65536}
	contextLengths.at[url] = time.Now()
	contextLengths.Unlock()
	st := decode[statsResponse](t, apiReq(t, srv, "GET", "/api/sessions/"+snap.ID+"/stats", nil))
	if want := promptBudget(65536); st.ContextBudget != want {
		t.Fatalf("context budget = %d, want %d (the model window, not the default)", st.ContextBudget, want)
	}
}

func TestWebCompactAndStats(t *testing.T) {
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	base := "/api/sessions/" + snap.ID

	// Nothing to compact in a fresh session.
	if resp := apiReq(t, srv, "POST", base+"/compact", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("compact of an empty session: %d, want 409", resp.StatusCode)
	}

	// Stats start at zero and name the subagent model that would be used.
	st := decode[statsResponse](t, apiReq(t, srv, "GET", base+"/stats", nil))
	if st.PromptTokens != 0 || st.Subagent.Running || st.Subagent.Model != "test-model" || st.ContextBudget <= 0 {
		t.Fatalf("fresh stats = %+v", st)
	}

	// Three turns, then a compaction that resets the page's transcript.
	events := openEvents(t, srv, snap.ID, snap.Since, "")
	for _, text := range []string{"first question", "second question", "third question"} {
		apiReq(t, srv, "POST", base+"/messages", map[string]string{"text": text}).Body.Close()
		events.until(t, EvDone)
	}
	for i := 0; i < 50; i++ { // the run flag clears just after the done event
		if !decode[sessionSnapshot](t, apiReq(t, srv, "GET", base, nil)).Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st = decode[statsResponse](t, apiReq(t, srv, "GET", base+"/stats", nil))
	if st.Turns != 3 || st.ContextUsed == 0 {
		t.Fatalf("stats after three turns = %+v", st)
	}
	res := decode[map[string]int](t, apiReq(t, srv, "POST", base+"/compact", nil))
	if res["messagesAfter"] >= res["messagesBefore"] || res["messagesBefore"] == 0 {
		t.Fatalf("compact result = %v", res)
	}
	if ev := events.next(t); ev.Type != evReset {
		t.Fatalf("after compact got %s, want reset", ev.Type)
	}
	// The session is usable again afterwards.
	apiReq(t, srv, "POST", base+"/messages", map[string]string{"text": "fourth"}).Body.Close()
	events.until(t, EvDone)
}
