package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A tool call is shown as one line - "▸ bash  git log --oneline  ✓ 0.4s" -
// with its output hidden until the user turns on Ctrl+O. Tool output (a
// loaded skill, a long diff, a directory listing) used to flood the
// transcript and bury the conversation.

type toolStatus int

const (
	toolRunning   toolStatus = iota
	toolPending              // waiting for the user's approval
	toolDone                 // finished
	toolFailed               // finished with an error result
	toolDenied               // the user said no
	toolBlocked              // refused by the permission policy
	toolCancelled            // the run ended before it finished
)

// toolEntry is one tool call in the transcript, updated in place as its
// approval and result arrive.
type toolEntry struct {
	id, name, args, reason string
	status                 toolStatus
	diff                   []DiffLine
	output                 string
	started                time.Time
	dur                    time.Duration
}

// toolSummary picks the part of a call's arguments worth one line: the
// command, path, query, URL, task or skill name.
func toolSummary(args string) string {
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) == nil {
		for _, key := range []string{"command", "path", "query", "url", "task", "name"} {
			if v, ok := m[key].(string); ok && v != "" {
				if f, ok := m["file"].(string); ok && key == "name" && f != "" {
					v += " " + f
				}
				return oneLine(v)
			}
		}
	}
	return oneLine(args)
}

// oneLine joins the lines of s so it fits on a single row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ⏎ ")), " ")
}

// shorten cuts s to at most n runes, ending in an ellipsis.
func shorten(s string, n int) string {
	r := []rune(s)
	if n < 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func formatDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

func (t *toolEntry) statusText() (string, bool) {
	switch t.status {
	case toolPending:
		return "? awaiting approval", false
	case toolDone:
		return "✓ " + formatDur(t.dur), false
	case toolFailed:
		return "✗ " + formatDur(t.dur), true
	case toolDenied:
		return "✗ denied", true
	case toolBlocked:
		return "⊘ blocked by policy", true
	case toolCancelled:
		return "– cancelled", false
	}
	return "…", false
}

// renderToolEntry draws a tool call: its one-line summary, then the diff for
// file edits (always, since it is the change being made), the policy reason
// while it matters, and the output only when expanded.
func renderToolEntry(t *toolEntry, width int, expanded bool) string {
	status, bad := t.statusText()
	marker := "▸"
	if expanded && t.output != "" {
		marker = "▾"
	}
	statusStyled := styleDim.Render(status)
	if bad {
		statusStyled = styleErr.Render(status)
	}
	if t.status == toolPending {
		statusStyled = accentApproval.Render(status)
	}
	room := width - len([]rune(t.name)) - len([]rune(status)) - 8
	if room < 8 {
		room = 8
	}
	head := accentTool.Render(marker+" "+t.name) + "  " + shorten(toolSummary(t.args), room) + "  " + statusStyled

	var b strings.Builder
	b.WriteString(head)
	if (t.status == toolPending || t.status == toolBlocked) && t.reason != "" {
		b.WriteString("\n" + accentBlock(accentApproval, t.reason, width))
	}
	if len(t.diff) > 0 {
		b.WriteString("\n" + strings.TrimRight(renderDiffLines(t.diff), "\n"))
	}
	if expanded && t.output != "" {
		b.WriteString("\n" + styleDim.Render(wrapToolResult(truncate(t.output, 2000), width)))
	}
	return b.String()
}

// turnFooter is the dim summary line closing a turn.
func turnFooter(toolCalls int, elapsed time.Duration, host string) string {
	var parts []string
	if toolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool call%s", toolCalls, plural(toolCalls)))
	}
	parts = append(parts, formatDur(elapsed))
	if host != "" {
		parts = append(parts, host)
	}
	return "  ⎿ " + strings.Join(parts, " · ")
}
