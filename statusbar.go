package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The status bar is one line, always: what you are working with on the left
// (mode, model, context, host and speed) and the key hints on the right. When
// the terminal is too narrow, hints are dropped from the end of the list, least
// important last, and only then is the left side shortened. A wrapped status
// bar pushes the whole layout down a row.

// keyHints are listed most important first.
func keyHints(streaming bool) []string {
	hints := []string{"/ commands", "Ctrl+O output", "Tab plan/build", "Ctrl+A auto/manual", "Ctrl+T thinking", "PgUp/PgDn scroll"}
	if streaming {
		hints = append([]string{"Ctrl+C cancel"}, hints...)
	}
	return hints
}

// statusLeft is the left side of the bar. The provider is named only when it
// is not the default llama-swap, and the round counter only while the model is
// in a tool-calling round.
func statusLeft(st Settings, round, used, budget int, host string) string {
	model := st.Model
	if st.Provider != "" && st.Provider != "llama-swap" {
		model = st.Provider + "/" + model
	}
	parts := []string{fmt.Sprintf("[%s] %s", modeLabel(st), model), fmt.Sprintf("ctx %s/%s", humanTokens(used), humanTokens(budget))}
	if round > 0 {
		parts = append(parts, fmt.Sprintf("round %d/%d", round, st.MaxRounds))
	}
	if host != "" {
		parts = append(parts, host)
	}
	return strings.Join(parts, " · ")
}

// statusBar lays out left and as many hints as fit in width columns (the bar's
// own padding included), as plain text with no line break.
func statusBar(width int, left string, hints []string) string {
	inner := width - 2
	if inner < 10 {
		inner = 10
	}
	for n := len(hints); n >= 0; n-- {
		right := strings.Join(hints[:n], "  ")
		sep := 0
		if n > 0 {
			sep = 2
		}
		if gap := inner - lipgloss.Width(left) - lipgloss.Width(right); gap >= sep {
			return left + strings.Repeat(" ", gap) + right
		}
	}
	return shorten(left, inner)
}

// subagentLabel is the status bar's note about a running spawn_agent
// subagent ("subagent minicpm5_2B · 4 calls"); empty when none is running.
func subagentLabel(st SessionStats) string {
	if !st.SubRunning {
		return ""
	}
	if st.SubActive > 1 {
		return fmt.Sprintf("%d subagents · %d call%s", st.SubActive, st.SubCalls, plural(st.SubCalls))
	}
	return fmt.Sprintf("subagent %s · %d call%s", st.SubModel, st.SubCalls, plural(st.SubCalls))
}
