package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestTypedRenameRunsAsACommand(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/rename Typed name")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if l, _ := m.sess.Label(); l != "Typed name" || m.state != stateIdle {
		t.Fatalf("label=%q state=%v", l, m.state)
	}
}
