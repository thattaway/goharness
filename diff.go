package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sergi/go-diff/diffmatchpatch"
)

const maxDiffLines = 40

var (
	styleDiffAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleDiffDel = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleDiffCtx = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
)

// DiffLine is one line of a line-level diff, kept as plain data so the TUI
// and the web UI can each style it their own way. Op is "+", "-" or " ".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// diffLines computes a line-level diff between old and new content.
func diffLines(oldText, newText string) []DiffLine {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lines)

	var out []DiffLine
	for _, d := range diffs {
		text := strings.TrimSuffix(d.Text, "\n")
		if text == "" {
			continue
		}
		op := " "
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			op = "+"
		case diffmatchpatch.DiffDelete:
			op = "-"
		}
		for _, line := range strings.Split(text, "\n") {
			out = append(out, DiffLine{Op: op, Text: line})
		}
	}
	return out
}

// renderDiffLines styles a diff for terminal display, capped so one huge
// file write can't flood the transcript.
func renderDiffLines(lines []DiffLine) string {
	var out strings.Builder
	for i, l := range lines {
		if i >= maxDiffLines {
			out.WriteString(styleDiffCtx.Render(fmt.Sprintf("  … (%d more diff lines)", len(lines)-i)))
			out.WriteString("\n")
			break
		}
		switch l.Op {
		case "+":
			out.WriteString(styleDiffAdd.Render("  + " + l.Text))
		case "-":
			out.WriteString(styleDiffDel.Render("  - " + l.Text))
		default:
			out.WriteString(styleDiffCtx.Render("    " + l.Text))
		}
		out.WriteString("\n")
	}
	return out.String()
}

// renderDiff produces a styled line-level diff between old and new content.
func renderDiff(oldText, newText string) string {
	return renderDiffLines(diffLines(oldText, newText))
}
