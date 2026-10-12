package main

import (
	"strings"
	"testing"
)

// Small models sometimes claim they are "in a simulated environment" and hand
// the user commands instead of running them. The prompt has to say the shell
// is real and that requested actions should be carried out.
func TestSystemPromptSaysShellIsRealAndToRunCommands(t *testing.T) {
	for _, st := range []Settings{{}, {Plan: true}, {Auto: true}} {
		p := systemPrompt("/proj", st)
		for _, want := range []string{
			"real machine",
			"not a simulation",
			"restart",
			"do it with bash",
			"call them anyway",
			"systemctl --user",
			"blocked by policy",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("system prompt (%+v) is missing %q", st, want)
			}
		}
	}
}
