package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWaitingMessageSaysLoadingWhenModelIsStarting(t *testing.T) {
	running := []runningModel{{Model: "gemma", State: "starting"}}
	got := waitingMessage(running, "gemma", 42*time.Second)
	if !strings.Contains(got, "loading gemma") || !strings.Contains(got, "42s") {
		t.Fatalf("got %q", got)
	}
}

func TestWaitingMessageSaysSwappingWhenAnotherModelIsLoaded(t *testing.T) {
	running := []runningModel{{Model: "other", State: "ready"}}
	got := waitingMessage(running, "gemma", 8*time.Second)
	if !strings.Contains(got, "swapping") || !strings.Contains(got, "gemma") {
		t.Fatalf("got %q", got)
	}
}

func TestWaitingMessageIsGenericWhenNothingIsKnown(t *testing.T) {
	got := waitingMessage(nil, "gemma", 5*time.Second)
	if !strings.Contains(got, "waiting for gemma") || !strings.Contains(got, "5s") {
		t.Fatalf("got %q", got)
	}
	// the model is loaded and ready: the delay is prompt processing
	ready := []runningModel{{Model: "gemma", State: "ready"}}
	if got := waitingMessage(ready, "gemma", 5*time.Second); !strings.Contains(got, "processing") {
		t.Fatalf("ready model should say it is processing, got %q", got)
	}
}

func TestFetchRunningReadsLlamaSwap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/running" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"running":[{"model":"gemma","state":"starting"}]}`))
	}))
	defer srv.Close()
	got := fetchRunning(srv.URL + "/v1")
	if len(got) != 1 || got[0].Model != "gemma" || got[0].State != "starting" {
		t.Fatalf("got %+v", got)
	}
	if got := fetchRunning(srv.URL + "/nope/v1"); got != nil {
		t.Fatalf("a server without /running should give nil, got %+v", got)
	}
}

type eventLog struct {
	mu sync.Mutex
	ev []Event
}

func (l *eventLog) sink(e Event) { l.mu.Lock(); l.ev = append(l.ev, e); l.mu.Unlock() }
func (l *eventLog) snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.ev...)
}

func TestWatchFirstByteReportsWhileWaitingThenClears(t *testing.T) {
	oldAfter, oldEvery := waitStatusAfter, waitStatusEvery
	waitStatusAfter, waitStatusEvery = 10*time.Millisecond, 10*time.Millisecond
	defer func() { waitStatusAfter, waitStatusEvery = oldAfter, oldEvery }()

	var l eventLog
	done := watchFirstByte(context.Background(), "http://127.0.0.1:1/v1", "gemma", l.sink)
	time.Sleep(80 * time.Millisecond)
	done()
	n := len(l.snapshot())
	if n < 3 {
		t.Fatalf("expected status updates while waiting, got %d events", n)
	}
	evs := l.snapshot()
	if evs[0].Type != EvStatus || !strings.Contains(evs[0].Text, "gemma") {
		t.Fatalf("first event = %+v", evs[0])
	}
	if last := evs[len(evs)-1]; last.Type != EvStatus || last.Text != "" {
		t.Fatalf("the last event must clear the status, got %+v", last)
	}
	time.Sleep(40 * time.Millisecond)
	if len(l.snapshot()) != n {
		t.Fatal("events kept coming after done()")
	}
}

func TestWatchFirstByteIsSilentWhenTheModelAnswersQuickly(t *testing.T) {
	var l eventLog
	done := watchFirstByte(context.Background(), "http://127.0.0.1:1/v1", "gemma", l.sink)
	done()
	if n := len(l.snapshot()); n != 0 {
		t.Fatalf("a fast response should show nothing, got %d events", n)
	}
}

func TestSessionRemembersWhichHostAnsweredAndTheSpeed(t *testing.T) {
	llm, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "hello there friend"} })
	llm.endpoint, llm.completionTokens = "artemis-llama-swap", 20
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "hi", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	st := s.Stats()
	if st.LastEndpoint != "artemis" {
		t.Fatalf("LastEndpoint = %q, want artemis", st.LastEndpoint)
	}
	if st.LastTPS <= 0 {
		t.Fatalf("LastTPS = %v", st.LastTPS)
	}
}

func TestHostLabelUsesTheBaseURLHostOrAFriendlyName(t *testing.T) {
	t.Setenv("GOHARNESS_HOST_NAMES", "")
	if got := hostLabel("http://172.16.10.40:8034/v1"); got != "172.16.10.40" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("GOHARNESS_HOST_NAMES", "172.16.10.45=artemis, 172.16.10.40 = athena")
	if got := hostLabel("http://172.16.10.40:8034/v1"); got != "athena" {
		t.Fatalf("got %q", got)
	}
	if got := hostLabel("http://172.16.10.99:1/v1"); got != "172.16.10.99" {
		t.Fatalf("unmapped host should stay as is, got %q", got)
	}
	if got := hostLabel("not a url"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSessionWithoutEndpointHeaderStillReportsSpeed(t *testing.T) {
	llm, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "a b c"} })
	llm.completionTokens = 5
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "hi", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(); st.LastEndpoint != "127.0.0.1" || st.LastTPS <= 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestStatusLineShowsHostAndSpeed(t *testing.T) {
	if got := hostSpeedLabel(SessionStats{LastEndpoint: "artemis", LastTPS: 13.44}); got != "artemis 13.4 tok/s" {
		t.Fatalf("got %q", got)
	}
	if got := hostSpeedLabel(SessionStats{LastTPS: 9}); got != "9.0 tok/s" {
		t.Fatalf("got %q", got)
	}
	if got := hostSpeedLabel(SessionStats{}); got != "" {
		t.Fatalf("no calls yet should give empty, got %q", got)
	}
}
