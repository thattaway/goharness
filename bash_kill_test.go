package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A command that leaves a background child holding stdout used to hang the
// bash tool forever: the timeout (or Ctrl+C) killed only the shell, and the
// tool then waited for a pipe the surviving child kept open.

func runBashWithin(t *testing.T, ctx context.Context, command string, limit time.Duration) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- toolBash(ctx, t.TempDir(), command) }()
	select {
	case out := <-done:
		return out
	case <-time.After(limit):
		t.Fatalf("toolBash still blocked after %s: a surviving child kept the pipe open", limit)
		return ""
	}
}

func TestBashTimeoutKillsBackgroundChildren(t *testing.T) {
	old := bashTimeout
	bashTimeout = time.Second
	t.Cleanup(func() { bashTimeout = old })

	out := runBashWithin(t, context.Background(), "sleep 30 & sleep 30", 10*time.Second)
	if !strings.Contains(out, "Timed out") {
		t.Errorf("out = %q, want a timeout message", out)
	}
}

func TestBashCancelKillsBackgroundChildren(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	out := runBashWithin(t, ctx, "sleep 30 & sleep 30", 10*time.Second)
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("out = %q, want a cancellation message", out)
	}
}
