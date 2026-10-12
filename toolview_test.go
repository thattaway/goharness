package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func manyLines(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix + " line\n")
	}
	return b.String()
}

func TestToolSummaryPicksTheUsefulArgument(t *testing.T) {
	cases := map[string]string{
		`{"command":"git log --oneline | head -3"}`: "git log --oneline | head -3",
		`{"path":"main.go"}`:                        "main.go",
		`{"name":"using-superpowers"}`:              "using-superpowers",
		`{"name":"book","file":"chapters/ch1.md"}`:  "book chapters/ch1.md",
		`{"command":"echo a\necho b"}`:              "echo a ⏎ echo b",
		`{"query":"go generics"}`:                   "go generics",
		`not json at all`:                           "not json at all",
	}
	for args, want := range cases {
		if got := toolSummary(args); got != want {
			t.Errorf("toolSummary(%s) = %q, want %q", args, got, want)
		}
	}
}

func TestToolCallIsOneLineUntilExpanded(t *testing.T) {
	m := readyModel(t)
	m.state = stateStreaming
	m = m.handleEvent(Event{Type: EvToolCall, ID: "1", Name: "bash", Args: `{"command":"ls -la"}`, Action: "allow"})
	m = m.handleEvent(Event{Type: EvToolResult, ID: "1", Output: manyLines("FILEROW", 30)})
	if !strings.Contains(m.transcript, "bash") || !strings.Contains(m.transcript, "ls -la") || !strings.Contains(m.transcript, "✓") {
		t.Fatalf("one-line summary missing:\n%s", m.transcript)
	}
	if strings.Contains(m.transcript, "FILEROW") {
		t.Fatalf("output should be hidden by default:\n%s", m.transcript)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	if !strings.Contains(m.transcript, "FILEROW") {
		t.Fatal("Ctrl+O should reveal the tool output")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if strings.Contains(next.(model).transcript, "FILEROW") {
		t.Fatal("a second Ctrl+O should hide it again")
	}
}

func TestLoadedSkillShowsItsNameNotItsText(t *testing.T) {
	m := readyModel(t)
	m = m.handleEvent(Event{Type: EvToolCall, ID: "s", Name: "skill", Args: `{"name":"using-superpowers"}`, Action: "allow"})
	m = m.handleEvent(Event{Type: EvToolResult, ID: "s", Output: "<EXTREMELY-IMPORTANT> huge skill text"})
	if !strings.Contains(m.transcript, "using-superpowers") || strings.Contains(m.transcript, "EXTREMELY-IMPORTANT") {
		t.Fatalf("transcript:\n%s", m.transcript)
	}
}

func TestToolCallAwaitingApprovalThenDenied(t *testing.T) {
	m := readyModel(t)
	m = m.handleEvent(Event{Type: EvToolCall, ID: "a", Name: "bash", Args: `{"command":"rm -rf build"}`, Action: "ask", Reason: "deletes files"})
	if !strings.Contains(m.transcript, "awaiting approval") || !strings.Contains(m.transcript, "deletes files") {
		t.Fatalf("pending call should say so and why:\n%s", m.transcript)
	}
	m = m.handleEvent(Event{Type: EvApproval, ID: "a", Approved: false})
	if !strings.Contains(m.transcript, "denied") || strings.Contains(m.transcript, "awaiting approval") {
		t.Fatalf("after a no it should read denied:\n%s", m.transcript)
	}
}

func TestPolicyBlockedCallIsMarked(t *testing.T) {
	m := readyModel(t)
	m = m.handleEvent(Event{Type: EvToolCall, ID: "b", Name: "bash", Args: `{"command":"sudo x"}`, Action: "deny", Reason: "sudo is blocked"})
	if !strings.Contains(m.transcript, "blocked by policy") || !strings.Contains(m.transcript, "sudo is blocked") {
		t.Fatalf("transcript:\n%s", m.transcript)
	}
}

func TestFailedToolResultIsMarkedFailed(t *testing.T) {
	m := readyModel(t)
	m = m.handleEvent(Event{Type: EvToolCall, ID: "f", Name: "read_file", Args: `{"path":"nope.txt"}`, Action: "allow"})
	m = m.handleEvent(Event{Type: EvToolResult, ID: "f", Output: "Error: open nope.txt: no such file"})
	if !strings.Contains(m.transcript, "✗") {
		t.Fatalf("an error result should show ✗:\n%s", m.transcript)
	}
}

func TestFileEditDiffStaysVisible(t *testing.T) {
	m := readyModel(t)
	m = m.handleEvent(Event{Type: EvToolCall, ID: "w", Name: "write_file", Args: `{"path":"a.txt"}`, Action: "allow",
		Diff: []DiffLine{{Op: "+", Text: "NEWCONTENT"}}})
	if !strings.Contains(m.transcript, "NEWCONTENT") {
		t.Fatalf("the diff is the change being made and must stay visible:\n%s", m.transcript)
	}
}

func TestUnfinishedToolCallsAreMarkedWhenTheRunEnds(t *testing.T) {
	m := readyModel(t)
	m.state = stateStreaming
	m = m.handleEvent(Event{Type: EvToolCall, ID: "c", Name: "bash", Args: `{"command":"sleep 99"}`, Action: "allow"})
	m = m.handleEvent(Event{Type: EvDone})
	if !strings.Contains(m.transcript, "cancelled") {
		t.Fatalf("transcript:\n%s", m.transcript)
	}
}

func TestTurnEndsWithAFooterAndStartsWithADivider(t *testing.T) {
	m := readyModel(t)
	m.appendUserBubble("first")
	m.turnStart, m.turnTools = time.Now().Add(-12*time.Second), 3
	m.state = stateStreaming
	m = m.handleEvent(Event{Type: EvDone})
	if !strings.Contains(m.transcript, "3 tool calls") || !strings.Contains(m.transcript, "12s") {
		t.Fatalf("footer missing:\n%s", m.transcript)
	}
	if strings.Contains(m.transcript, "────") {
		t.Fatal("no divider before the very first turn")
	}
	m.appendUserBubble("second")
	if !strings.Contains(m.transcript, "────") {
		t.Fatalf("divider missing before the second turn:\n%s", m.transcript)
	}
}

func TestFooterWithoutToolCallsOmitsThatPart(t *testing.T) {
	got := turnFooter(0, 4*time.Second, "athena 14.5 tok/s")
	if strings.Contains(got, "tool call") || !strings.Contains(got, "4s") || !strings.Contains(got, "athena 14.5 tok/s") {
		t.Fatalf("got %q", got)
	}
	if got := turnFooter(1, 90*time.Second, ""); !strings.Contains(got, "1 tool call ") && !strings.Contains(got, "1 tool call·") && !strings.Contains(got, "1 tool call") || !strings.Contains(got, "1m30s") {
		t.Fatalf("got %q", got)
	}
}
