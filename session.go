package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".goharness", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func newSessionPath() (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s.json", time.Now().Format("2006-01-02T15-04-05"))
	return filepath.Join(dir, name), nil
}

// saveSession overwrites the session file with the current message history.
// Called after every completed turn - simple and correct beats clever here,
// since a TUI test harness doesn't need append-only durability guarantees.
func saveSession(path string, messages []Message) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadSession(path string) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var messages []Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// sessionMeta sits next to a session file (<name>.meta) and holds what the
// message list can't: which project the session works on, and the undo
// point for each turn.
type sessionMeta struct {
	ProjectRoot string     `json:"projectRoot,omitempty"`
	Title       string     `json:"title,omitempty"` // set by renaming; empty means "use the first message"
	Pinned      bool       `json:"pinned,omitempty"`
	Group       string     `json:"group,omitempty"`    // id of a user-made sidebar group (see groups.go); empty means the project's own
	Settings    *Settings  `json:"settings,omitempty"` // model and modes, so they survive restarts
	Turns       []turnMark `json:"turns,omitempty"`
}

// turnMark is where one turn began: the message index before its user
// message, and the checkpoint of the project's files at that moment.
type turnMark struct {
	MsgIndex   int    `json:"msgIndex"`
	Checkpoint string `json:"checkpoint,omitempty"`
	After      string `json:"after,omitempty"` // snapshot when the turn ended
	Files      int    `json:"files"`           // files the turn changed (0 when unknown)
	Text       string `json:"text"`
}

func metaPath(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".json") + ".meta"
}

func loadMeta(sessionPath string) sessionMeta {
	var m sessionMeta
	if sessionPath == "" {
		return m
	}
	if data, err := os.ReadFile(metaPath(sessionPath)); err == nil {
		json.Unmarshal(data, &m)
	}
	return m
}

func saveMeta(sessionPath string, m sessionMeta) error {
	if sessionPath == "" {
		return nil
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(sessionPath), data, 0o644)
}
