package main

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// trailingPad matches the styled spaces glamour appends to pad every line out
// to the wrap width; they only add width and hide real trailing content.
var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// Finished agent replies are rendered as styled terminal text instead of
// showing the raw markdown markers. A reply that is still streaming stays
// plain text (see refreshStream): partial markdown - an unclosed ** or code
// fence - renders badly, so it is only formatted once the reply is complete.

var latexSymbols = map[string]string{
	`\leftrightarrow`: "↔", `\Leftrightarrow`: "⇔", `\rightarrow`: "→", `\Rightarrow`: "⇒",
	`\leftarrow`: "←", `\Leftarrow`: "⇐", `\to`: "→", `\times`: "×", `\geq`: "≥", `\leq`: "≤",
	`\neq`: "≠", `\approx`: "≈", `\ldots`: "…", `\cdot`: "·", `\pm`: "±",
}

var inlineLatex = regexp.MustCompile(`\$\s*(\\[A-Za-z]+)\s*\$`)

// replaceLatexSymbols turns a lone-symbol inline math span like $\leftrightarrow$
// into the Unicode character. Anything more complex is left as written.
func replaceLatexSymbols(s string) string {
	return inlineLatex.ReplaceAllStringFunc(s, func(m string) string {
		if sym, ok := latexSymbols[inlineLatex.FindStringSubmatch(m)[1]]; ok {
			return sym
		}
		return m
	})
}

var (
	rendererMu sync.Mutex
	renderers  = map[int]*glamour.TermRenderer{}
)

// markdownRenderer caches one renderer per wrap width; building one is slow
// and the transcript is re-rendered on every resize and new message.
func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	if r, ok := renderers[width]; ok {
		return r, nil
	}
	style := styles.DarkStyleConfig
	zero := uint(0)
	style.Document.Margin = &zero
	// The stock style spells out "## ", "### " ... in front of headings,
	// which is exactly the raw markdown this is meant to hide. H1 keeps its
	// padded banner look.
	for _, h := range []*ansi.StyleBlock{&style.H2, &style.H3, &style.H4, &style.H5, &style.H6} {
		h.Prefix = ""
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		return nil, err
	}
	renderers[width] = r
	return r, nil
}

// renderMarkdown formats text for a terminal width, falling back to the
// original text if rendering fails so a reply is never lost.
func renderMarkdown(text string, width int) string {
	if width < 20 {
		width = 20
	}
	r, err := markdownRenderer(width)
	if err != nil {
		return text
	}
	out, err := r.Render(replaceLatexSymbols(text))
	if err != nil {
		return text
	}
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range lines {
		lines[i] = trailingPad.ReplaceAllString(l, "") + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
