package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(root, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, name))
	return string(b), err == nil
}

func TestCheckpointRestore(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"keep.txt":       "original",
		"gone.txt":       "will be deleted",
		"sub/nested.txt": "nested",
		".gitignore":     "ignored.log\n",
		"ignored.log":    "log v1",
		"node_modules/x": "dep",
	})
	cp, err := newCheckpointer(root)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := cp.snapshot("before")
	if err != nil {
		t.Fatal(err)
	}

	// The "turn": edit, delete, create, and touch an ignored file.
	writeFiles(t, root, map[string]string{"keep.txt": "edited", "new/created.txt": "new", "ignored.log": "log v2"})
	os.Remove(filepath.Join(root, "gone.txt"))

	changed, err := cp.changedSince(commit)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(changed)
	if got := strings.Join(changed, ","); got != "gone.txt,keep.txt,new/created.txt" {
		t.Fatalf("changed = %s", got)
	}

	if err := cp.restore(commit); err != nil {
		t.Fatal(err)
	}
	if s, _ := readFile(root, "keep.txt"); s != "original" {
		t.Errorf("keep.txt = %q", s)
	}
	if s, ok := readFile(root, "gone.txt"); !ok || s != "will be deleted" {
		t.Error("deleted file not restored")
	}
	if _, ok := readFile(root, "new/created.txt"); ok {
		t.Error("created file not removed")
	}
	if s, _ := readFile(root, "ignored.log"); s != "log v2" {
		t.Error("ignored file was touched")
	}
	if _, ok := readFile(root, "node_modules/x"); !ok {
		t.Error("excluded dir was touched")
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		t.Error("created a .git in the project")
	}
}

func TestCheckpointLeavesProjectGitAlone(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.txt": "1", ".git/HEAD": "ref: refs/heads/main\n", ".git/config": "[core]\n"})
	cp, _ := newCheckpointer(root)
	commit, err := cp.snapshot("before")
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"a.txt": "2", ".git/config": "[core]\n\tchanged = true\n"})
	if err := cp.restore(commit); err != nil {
		t.Fatal(err)
	}
	if s, _ := readFile(root, "a.txt"); s != "1" {
		t.Errorf("a.txt = %q", s)
	}
	if s, _ := readFile(root, ".git/config"); !strings.Contains(s, "changed") {
		t.Error("the project's own .git was rolled back")
	}
}

func TestCheckpointRefusesHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := newCheckpointer(home); err == nil {
		t.Fatal("agreed to snapshot the home directory")
	}
}

func TestCheckpointTooSlowTurnsOff(t *testing.T) {
	old := checkpointTimeout
	checkpointTimeout = time.Nanosecond
	t.Cleanup(func() { checkpointTimeout = old })

	_, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	s := NewSession(url, "m", t.TempDir(), "", nil)
	var c collector
	for i := 0; i < 2; i++ {
		if err := s.Run(context.Background(), "hi", nil, c.sink, noApprover(t)); err != nil {
			t.Fatal(err)
		}
	}
	notices := c.find(EvNotice)
	if len(notices) != 1 || !strings.Contains(notices[0].Text, "checkpoints are off") {
		t.Fatalf("notices = %+v", notices)
	}
	if s.cp != nil {
		t.Fatal("checkpointer still on")
	}
}
