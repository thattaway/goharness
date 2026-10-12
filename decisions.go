package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The decision log is an append-only JSONL record of every tool call the
// agent made and how it was decided: what the policy tier said, who allowed
// or denied it, and how it turned out. It exists to build up a labelled
// dataset over time (approvals and denials, failed calls) for evaluating or
// training small decision models - nothing in the agent reads it back.
//
// GOHARNESS_DECISION_LOG overrides the path (default
// ~/.goharness/decisions.jsonl); "off" disables it. Logging never fails a
// tool call: write errors are dropped.

const maxLoggedInput = 500

type decisionRecord struct {
	Time       string `json:"time"`
	Session    string `json:"session,omitempty"`
	Model      string `json:"model"`
	Tool       string `json:"tool"`
	Input      string `json:"input,omitempty"`    // bash command, or the file path / search query / URL
	Tier       string `json:"tier"`               // what the permission policy said: allow, ask or deny
	Decision   string `json:"decision"`           // allowed or denied
	By         string `json:"by"`                 // policy, plan-mode, auto-mode or user
	Reason     string `json:"reason,omitempty"`   // the policy's explanation, when it gave one
	Outcome    string `json:"outcome"`            // ok, error, cancelled or not-run
	Subagent   bool   `json:"subagent,omitempty"` // made by a spawn_agent subagent
	DurationMS int64  `json:"duration_ms,omitempty"`
}

var decisionMu sync.Mutex

func decisionLogPath() string {
	switch v := os.Getenv("GOHARNESS_DECISION_LOG"); strings.ToLower(v) {
	case "off", "0", "false", "no":
		return ""
	case "":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".goharness", "decisions.jsonl")
	default:
		return os.Getenv("GOHARNESS_DECISION_LOG")
	}
}

// loggedInput is the part of a call worth recording. File contents and other
// bulky arguments are left out, and anything that looks like a credential is
// replaced.
func loggedInput(tool string, args map[string]any) string {
	var in string
	for _, key := range []string{"command", "path", "query", "url", "task"} {
		if v, ok := args[key].(string); ok {
			in = v
			break
		}
	}
	if in == "" {
		return ""
	}
	if looksLikeSecret(in) || (tool == "bash" && commandMentionsSecret(in)) {
		return "[redacted]"
	}
	if len(in) > maxLoggedInput {
		in = in[:maxLoggedInput] + "..."
	}
	return in
}

func (s *Session) logDecision(r decisionRecord) {
	path := decisionLogPath()
	if path == "" {
		return
	}
	r.Time = time.Now().UTC().Format(time.RFC3339)
	if p := s.Path(); p != "" {
		r.Session = strings.TrimSuffix(filepath.Base(p), ".json")
	}
	r.Subagent = s.sub
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	decisionMu.Lock()
	defer decisionMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// outcomeOf classifies a tool's output text. The tools report failure as
// text rather than a separate error value.
func outcomeOf(out string) string {
	if strings.HasPrefix(out, "Error") || strings.HasPrefix(out, "Timed out after") {
		return "error"
	}
	if out == "Cancelled by the user." {
		return "cancelled"
	}
	return "ok"
}
