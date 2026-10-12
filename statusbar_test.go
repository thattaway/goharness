package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestStatusBarShowsEveryHintWhenThereIsRoom(t *testing.T) {
	left := "[Build/Auto] gemma · ctx 1k/23k"
	got := statusBar(200, left, keyHints(false))
	for _, h := range keyHints(false) {
		if !strings.Contains(got, h) {
			t.Fatalf("hint %q missing from %q", h, got)
		}
	}
	if !strings.HasPrefix(got, left) || lipgloss.Width(got) != 198 {
		t.Fatalf("hints should be right-aligned to the inner width, got width %d: %q", lipgloss.Width(got), got)
	}
}

func TestStatusBarDropsHintsFromTheEndBeforeEverythingElse(t *testing.T) {
	left := "[Build/Auto] gemma · ctx 1k/23k"
	hints := keyHints(false)
	got := statusBar(70, left, hints)
	if !strings.Contains(got, left) || !strings.Contains(got, "/ commands") {
		t.Fatalf("the left side and the first hint must survive: %q", got)
	}
	if strings.Contains(got, "PgUp/PgDn") {
		t.Fatalf("the last hint should go first: %q", got)
	}
	if lipgloss.Width(got) > 68 {
		t.Fatalf("too wide: %d", lipgloss.Width(got))
	}
}

func TestStatusBarNeverWrapsOrOverflows(t *testing.T) {
	left := "[Build+TDD/Auto] some-very-long-model-name · ctx 6.3k/23.1k · round 2/10 · athena 9.8 tok/s"
	for width := 12; width <= 220; width += 7 {
		for _, streaming := range []bool{false, true} {
			got := statusBar(width, left, keyHints(streaming))
			if strings.Contains(got, "\n") || lipgloss.Width(got) > width-2 {
				t.Fatalf("width %d streaming=%v: %q is %d wide", width, streaming, got, lipgloss.Width(got))
			}
		}
	}
}

func TestStatusBarShortensTheLeftSideOnlyAsALastResort(t *testing.T) {
	left := strings.Repeat("x", 100)
	got := statusBar(40, left, keyHints(false))
	if !strings.HasSuffix(got, "…") || lipgloss.Width(got) != 38 {
		t.Fatalf("got %q (%d wide)", got, lipgloss.Width(got))
	}
}

func TestCancelHintComesFirstWhileStreaming(t *testing.T) {
	if h := keyHints(true); h[0] != "Ctrl+C cancel" {
		t.Fatalf("hints = %v", h)
	}
	if h := keyHints(false); h[0] == "Ctrl+C cancel" {
		t.Fatal("no cancel hint when idle")
	}
}

func TestStatusLeftIsCompact(t *testing.T) {
	st := Settings{Model: "gemma", MaxRounds: 10}
	got := statusLeft(st, 0, 6300, 23100, "athena 9.8 tok/s")
	if got != "[Build/Manual] gemma · ctx 6.3k/23.1k · athena 9.8 tok/s" {
		t.Fatalf("got %q", got)
	}
	if got := statusLeft(st, 2, 100, 23100, ""); !strings.Contains(got, "round 2/10") || strings.Contains(got, "llama-swap") {
		t.Fatalf("got %q", got)
	}
	st.Provider = "freellmapi"
	if got := statusLeft(st, 0, 100, 23100, ""); !strings.Contains(got, "freellmapi/gemma") {
		t.Fatalf("a non-default provider should be named: %q", got)
	}
}

func TestViewStatusBarIsOneLine(t *testing.T) {
	m := readyModel(t)
	m.state = stateStreaming
	m.sess.stats.lastEndpoint, m.sess.stats.lastTPS = "athena", 9.8
	lines := strings.Split(m.View(), "\n")
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "ctx ") && strings.Contains(l, "Ctrl+C cancel") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected the status bar on exactly one line, found %d:\n%s", n, m.View())
	}
}

func TestHelpListsTheKeys(t *testing.T) {
	m := readyModel(t)
	m, _ = m.executeCommand("/help")
	for _, key := range []string{"Ctrl+T", "Ctrl+O", "Ctrl+A", "Tab", "PgUp"} {
		if !strings.Contains(m.transcript, key) {
			t.Fatalf("/help should list %s:\n%s", key, m.transcript)
		}
	}
}
