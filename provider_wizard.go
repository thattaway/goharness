package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// providerWizard is the step-by-step "/provider add" with no arguments: one question at a time in the input
// line, each answer checked as it is given, Esc to cancel. The one-line form still works.
type providerWizard struct {
	step        int
	p           provider
	placeholder string // the input line's placeholder before the wizard, put back afterwards
}

const (
	wzName = iota
	wzURL
	wzModel
	wzKeyEnv
	wzKey
)

var wizardAsks = [...]struct{ ask, hint string }{
	wzName:   {"Provider name", "lowercase letters, digits, . _ -  (for example: litellm)"},
	wzURL:    {"Base URL", "an OpenAI-compatible server, for example http://172.16.10.46:4000/v1"},
	wzModel:  {"Model", "the model id to use first (you can add more later by editing the file)"},
	wzKeyEnv: {"API key variable", "the name of an environment variable that holds the key, for example LITELLM_API_KEY (blank to type a key instead)"},
	wzKey:    {"API key", "typed hidden and stored in ~/.config/goharness/custom_providers.json (blank for none)"},
}

func (m *model) startProviderWizard() {
	m.wizard = &providerWizard{placeholder: m.input.Placeholder}
	m.input.Reset()
	m.appendLine(styleDim.Render("  Adding a provider. Answer each question and press Enter; Esc cancels."))
	m.askWizard()
}

// askWizard prints the current question and sets up the input line for it.
func (m *model) askWizard() {
	w := m.wizard
	q := wizardAsks[w.step]
	m.input.Placeholder = q.ask
	if w.step == wzKey {
		m.input.EchoMode = textinput.EchoPassword
	} else {
		m.input.EchoMode = textinput.EchoNormal
	}
	m.appendLine(styleDim.Render(fmt.Sprintf("  %s: %s", q.ask, q.hint)))
}

func (m *model) endWizard() {
	if m.wizard != nil {
		m.input.Placeholder = m.wizard.placeholder
	}
	m.input.EchoMode = textinput.EchoNormal
	m.input.Reset()
	m.wizard = nil
}

func (m *model) cancelWizard() {
	m.endWizard()
	m.appendLine(styleDim.Render("  provider setup cancelled; nothing was saved."))
}

// wizardEnter takes the answer typed in the input line.
func (m model) wizardEnter() (model, tea.Cmd) {
	w := m.wizard
	val := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	again := func(err error) (model, tea.Cmd) {
		m.appendLine(styleErr.Render("  " + err.Error() + " - try again, or Esc to cancel"))
		return m, nil
	}
	echo := func(label, v string) { m.appendLine(styleDim.Render(fmt.Sprintf("  %s: %s", label, v))) }
	switch w.step {
	case wzName:
		if err := validateProviderName(val); err != nil {
			return again(err)
		}
		for _, o := range otherProviders {
			if o.Name == val {
				return again(fmt.Errorf("provider %q already exists as a built-in", val))
			}
		}
		w.p.Name = val
		echo("name", val)
	case wzURL:
		if err := validateBaseURL(val); err != nil {
			return again(err)
		}
		w.p.BaseURL = strings.TrimRight(val, "/")
		echo("base URL", w.p.BaseURL)
	case wzModel:
		if val == "" {
			return again(fmt.Errorf("the model name can't be empty"))
		}
		w.p.Models = []string{val}
		echo("model", val)
	case wzKeyEnv:
		if val != "" {
			if !envVarRe.MatchString(val) {
				return again(fmt.Errorf("%q is not a valid environment variable name", val))
			}
			w.p.APIKeyEnv = val
			echo("key variable", val)
			if os.Getenv(val) == "" {
				m.appendLine(styleDim.Render("  note: " + val + " is not set where goharness runs; put " + val + "=... in ~/.config/goharness/env"))
			}
			return m.finishWizard()
		}
		echo("key variable", "(none)")
	case wzKey:
		w.p.APIKey = val
		if val == "" {
			echo("api key", "(none)")
		} else {
			echo("api key", "(hidden)")
		}
		return m.finishWizard()
	}
	w.step++
	m.askWizard()
	return m, nil
}

func (m model) finishWizard() (model, tea.Cmd) {
	p := m.wizard.p
	m.endWizard()
	if err := saveCustomProvider(p); err != nil {
		m.appendLine(styleErr.Render("  " + err.Error()))
		return m, nil
	}
	clearModelInfoCaches()
	m.appendLine(styleDim.Render(fmt.Sprintf("  provider %q added with model %q. /provider to switch, /model to pick.", p.Name, p.Models[0])))
	return m, nil
}
