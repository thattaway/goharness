package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownHidesMarkers(t *testing.T) {
	raw := renderMarkdown("### Next Steps\n\nThis is **bold** and `code`.\n\n1. one\n2. two\n", 60)
	out := ansi.Strip(raw)
	for _, marker := range []string{"###", "**", "`"} {
		if strings.Contains(out, marker) {
			t.Errorf("rendered output still contains raw marker %q:\n%s", marker, out)
		}
	}
	for _, want := range []string{"Next Steps", "bold", "code", "one", "two"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output lost text %q:\n%s", want, out)
		}
	}
}

func TestReplaceLatexSymbols(t *testing.T) {
	got := replaceLatexSymbols(`Frontend $\leftrightarrow$ Backend, a $\rightarrow$ b, $x^2$ stays`)
	want := `Frontend ↔ Backend, a → b, $x^2$ stays`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderMarkdownKeepsLongLinesWithinWidth(t *testing.T) {
	out := renderMarkdown(strings.Repeat("word ", 60), 40)
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(ansi.Strip(line))) > 40 {
			t.Errorf("line wider than 40 columns: %q", line)
		}
	}
}
