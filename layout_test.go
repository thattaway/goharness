package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// plainLines is the transcript as the user sees it, minus colour codes.
func plainLines(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

func lineWith(t *testing.T, lines []string, needle string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return l
		}
	}
	t.Fatalf("no line contains %q in:\n%s", needle, strings.Join(lines, "\n"))
	return ""
}

func TestUserMessageIsAHeaderOnTheLeft(t *testing.T) {
	m := readyModel(t)
	m.appendUserBubble("hello there")
	line := lineWith(t, plainLines(m.transcript), "hello there")
	if !strings.HasPrefix(line, "❯ hello there") {
		t.Fatalf("user message should be a left-aligned ❯ header, got %q", line)
	}
	if strings.Contains(m.transcript, " you ") {
		t.Fatal("the 'you' badge should be gone")
	}
}

func TestLongUserMessageWrapsWithAHangingIndent(t *testing.T) {
	m := readyModel(t)
	m.appendUserBubble(strings.Repeat("word ", 80))
	var body []string
	for _, l := range plainLines(m.transcript) {
		if strings.Contains(l, "word") {
			body = append(body, l)
		}
	}
	if len(body) < 3 {
		t.Fatalf("expected the message to wrap, got %d lines", len(body))
	}
	if !strings.HasPrefix(body[0], "❯ ") || !strings.HasPrefix(body[1], "  word") {
		t.Fatalf("continuation lines should be indented under the text: %q / %q", body[0], body[1])
	}
	for _, l := range body {
		if w := len([]rune(l)); w > bubbleWidth(m) {
			t.Fatalf("line is %d wide, wider than the transcript (%d)", w, bubbleWidth(m))
		}
	}
}

func TestAgentReplyHasALeftRuleAndNoBadge(t *testing.T) {
	m := readyModel(t)
	m.appendAgentBubble("First paragraph.\n\nSecond paragraph.", "model-alpha")
	lines := plainLines(m.transcript)
	for _, needle := range []string{"First paragraph.", "Second paragraph."} {
		if l := lineWith(t, lines, needle); !strings.HasPrefix(l, "┃ ") {
			t.Fatalf("reply line %q should start with the rule", l)
		}
	}
	if strings.Contains(m.transcript, " agent ") {
		t.Fatal("the 'agent' badge should be gone")
	}
	// the rule runs unbroken through the blank line between paragraphs
	first, second := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "First paragraph.") {
			first = i
		}
		if strings.Contains(l, "Second paragraph.") {
			second = i
		}
	}
	for i := first + 1; i < second; i++ {
		if !strings.HasPrefix(lines[i], "┃") {
			t.Fatalf("line %d between paragraphs lost the rule: %q", i, lines[i])
		}
	}
}

func TestModelNameIsShownOnlyWhenItChanges(t *testing.T) {
	m := readyModel(t)
	m.appendAgentBubble("one", "model-alpha")
	m.appendAgentBubble("two", "model-alpha")
	m.appendAgentBubble("three", "model-beta")
	m.appendAgentBubble("four", "model-beta")
	out := ansi.Strip(m.transcript)
	if n := strings.Count(out, "model-alpha"); n != 1 {
		t.Fatalf("model-alpha shown %d times, want once", n)
	}
	if n := strings.Count(out, "model-beta"); n != 1 {
		t.Fatalf("model-beta shown %d times, want once (it changed)", n)
	}
}

func TestStreamingReplyLooksLikeAFinishedOne(t *testing.T) {
	m := readyModel(t)
	m.appendAgentBubble("earlier reply", "model-alpha")
	m.streamingContent = "partial answer"
	m.refreshStream()
	view := ansi.Strip(m.viewport.View())
	if l := lineWith(t, strings.Split(view, "\n"), "partial answer"); !strings.HasPrefix(l, "┃ partial answer") {
		t.Fatalf("streaming text should carry the rule: %q", l)
	}
	if strings.Count(view, "model-alpha") != 1 {
		t.Fatalf("the model name must not repeat for the same model while streaming:\n%s", view)
	}
}
