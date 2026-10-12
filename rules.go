package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// User permission rules for bash commands, kept in
// ~/.config/goharness/permissions.json and editable from the web UI. They
// extend the built-in rules and win when both match. They can't switch off
// the other safeguards: a command that redirects output, substitutes
// commands or touches a secret file still asks even if a rule allows it.
// The file sits in a directory the agent is not allowed to write to, so it
// can't grant itself permissions.

type Rule struct {
	Pattern string `json:"pattern"`
	Action  string `json:"action"` // allow, ask or deny
}

var userRules struct {
	sync.RWMutex
	rules []Rule
}

func currentUserRules() []Rule {
	userRules.RLock()
	defer userRules.RUnlock()
	return userRules.rules
}

func rulesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "goharness", "permissions.json"), nil
}

// defaultRules lists the built-in rules in evaluation order.
func defaultRules() []Rule {
	out := make([]Rule, 0, len(bashRuleOrder))
	for _, p := range bashRuleOrder {
		out = append(out, Rule{Pattern: p, Action: bashRules[p]})
	}
	return out
}

func validateRules(rules []Rule) ([]Rule, error) {
	if len(rules) > 200 {
		return nil, errors.New("too many rules (200 max)")
	}
	clean := make([]Rule, 0, len(rules))
	for i, r := range rules {
		r.Pattern = strings.TrimSpace(r.Pattern)
		switch {
		case r.Pattern == "":
			return nil, fmt.Errorf("rule %d: pattern is empty", i+1)
		case len(r.Pattern) > 200:
			return nil, fmt.Errorf("rule %d: pattern is too long", i+1)
		case r.Action != "allow" && r.Action != "ask" && r.Action != "deny":
			return nil, fmt.Errorf("rule %d: action must be allow, ask or deny", i+1)
		}
		clean = append(clean, r)
	}
	return clean, nil
}

// loadUserRules reads permissions.json; a missing file means no rules.
func loadUserRules() error {
	path, err := rulesPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	rules, err := validateRules(file.Rules)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	userRules.Lock()
	userRules.rules = rules
	userRules.Unlock()
	return nil
}

// saveUserRules validates, writes and applies a new rule list.
func saveUserRules(rules []Rule) ([]Rule, error) {
	rules, err := validateRules(rules)
	if err != nil {
		return nil, err
	}
	path, err := rulesPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(map[string]any{"rules": rules}, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	userRules.Lock()
	userRules.rules = rules
	userRules.Unlock()
	return rules, nil
}
