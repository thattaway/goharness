package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrReplace(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "f.txt")
	write := func(s string) { os.WriteFile(f, []byte(s), 0o644) }
	read := func() string { b, _ := os.ReadFile(f); return string(b) }

	write("a x b x c")
	if out := toolStrReplace(f, "x", "y", false); !strings.Contains(out, "2 times") || read() != "a x b x c" {
		t.Fatalf("ambiguous match: %q, file %q", out, read())
	}
	if out := toolStrReplace(f, "b x", "b y", false); !strings.HasPrefix(out, "Successfully") || read() != "a x b y c" {
		t.Fatalf("unique match: %q, file %q", out, read())
	}
	if out := toolStrReplace(f, "x", "z", true); !strings.Contains(out, "2") && read() != "a z b y c" {
		t.Fatalf("multi: %q, file %q", out, read())
	}
	if out := toolStrReplace(f, "nope", "z", false); !strings.Contains(out, "not found") {
		t.Fatalf("missing: %q", out)
	}
	if out := toolStrReplace(f, "", "z", false); !strings.HasPrefix(out, "Error") {
		t.Fatalf("empty old_str: %q", out)
	}
}

func TestToolsUseProjectRoot(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "here.txt"), []byte("hi"), 0o644)
	ctx := context.Background()
	if out := runTool(ctx, root, "read_file", map[string]any{"path": "here.txt"}); out != "hi" {
		t.Fatalf("relative read_file = %q", out)
	}
	if out := runTool(ctx, root, "bash", map[string]any{"command": "pwd"}); strings.TrimSpace(out) != root {
		t.Fatalf("bash pwd = %q, want %q", out, root)
	}
	runTool(ctx, root, "write_file", map[string]any{"path": "sub.txt", "content": "x"})
	if _, err := os.Stat(filepath.Join(root, "sub.txt")); err != nil {
		t.Fatal("relative write_file did not land in the project")
	}
}
