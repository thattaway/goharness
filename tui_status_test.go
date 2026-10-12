package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func readyModel(t *testing.T) model {
	t.Helper()
	_, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	s, _ := newTestSession(t, url)
	var m tea.Model = initialModel(s)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m.(model)
}

func TestTUIShowsWaitingStatusUntilTheFirstToken(t *testing.T) {
	m := readyModel(t)
	m.state = stateStreaming
	m = m.handleEvent(Event{Type: EvStatus, Text: "loading gemma… 12s"})
	if v := m.View(); !strings.Contains(v, "loading gemma… 12s") {
		t.Fatalf("status missing from view:\n%s", v)
	}
	m = m.handleEvent(Event{Type: EvDelta, Text: "Hi"})
	if v := m.View(); strings.Contains(v, "loading gemma") {
		t.Fatal("status should clear on the first streamed token")
	}
}

func TestTUIStatusBarShowsHostAndSpeedAfterACall(t *testing.T) {
	m := readyModel(t)
	m.sess.stats.lastEndpoint, m.sess.stats.lastTPS = "artemis", 13.4
	if v := m.View(); !strings.Contains(v, "artemis 13.4 tok/s") {
		t.Fatalf("host and speed missing from view:\n%s", v)
	}
}

func TestTUICompactWithNothingToCompactSaysSo(t *testing.T) {
	m := readyModel(t)
	m, cmd := m.executeCommand("/compact")
	if m.state != stateStreaming || cmd == nil {
		t.Fatalf("state=%v cmd=%v", m.state, cmd)
	}
	done, ok := cmd().(compactDoneMsg)
	if !ok || done.err == nil {
		t.Fatalf("got %#v", done)
	}
	next, _ := m.Update(done)
	m = next.(model)
	if m.state != stateIdle || m.statusText != "" || m.cancelFunc != nil {
		t.Fatalf("model not back to idle: %+v", m.state)
	}
}

func TestTUIShowsARunningSubagent(t *testing.T) {
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	m := readyModel(t)
	if err := m.sess.SetSettings(Settings{Model: "main-model", SubagentModel: "small-model", MaxRounds: 10}); err != nil {
		t.Fatal(err)
	}

	// Idle: the sidebar names the configured subagent model, the status bar says nothing.
	v := m.View()
	if !strings.Contains(v, "small-model") || !strings.Contains(v, "idle") {
		t.Fatalf("idle sidebar should show the subagent model and 'idle':\n%s", v)
	}
	if strings.Contains(v, "subagent small-model") {
		t.Fatal("status bar must not mention a subagent while none is running")
	}

	// Running: both places show the model and the tool calls so far.
	m.sess.subagentStarted("small-model")
	m.sess.subagentTool()
	m.sess.subagentTool()
	v = m.View()
	if !strings.Contains(v, "subagent small-model · 2 calls") {
		t.Fatalf("status bar should show the running subagent:\n%s", v)
	}
	if !strings.Contains(v, "running: small-model") || !strings.Contains(v, "2 tool calls") {
		t.Fatalf("sidebar should show the running subagent:\n%s", v)
	}

	// Finished: back to idle, with a run count.
	m.sess.subagentFinished()
	v = m.View()
	if strings.Contains(v, "subagent small-model · ") {
		t.Fatal("status bar should clear when the subagent finishes")
	}
	if !strings.Contains(v, "1 run done") {
		t.Fatalf("sidebar should count finished runs:\n%s", v)
	}
}

func TestSubagentLabel(t *testing.T) {
	if got := subagentLabel(SessionStats{}); got != "" {
		t.Fatalf("idle label = %q, want empty", got)
	}
	if got := subagentLabel(SessionStats{SubRunning: true, SubModel: "m", SubCalls: 1}); got != "subagent m · 1 call" {
		t.Fatalf("label = %q", got)
	}
}
