package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePython stands in for the Scrapling interpreter: it ignores the piped
// script and prints canned output, or fails.
func fakePython(t *testing.T, body string) {
	p := filepath.Join(t.TempDir(), "python")
	os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	t.Setenv("SCRAPLING_PYTHON", p)
}

func TestFetchRejectsBadURLs(t *testing.T) {
	for _, u := range []string{"", "example.com", "file:///etc/passwd", "ftp://x.org/a", "http://"} {
		if out := toolFetchPage(context.Background(), u, "", 0, 0); !strings.HasPrefix(out, "Error: 'url'") {
			t.Errorf("%q not rejected: %s", u, out)
		}
	}
}

func TestFetchSuccessIsMarkedUntrusted(t *testing.T) {
	fakePython(t, `echo "Title: T"; echo "URL: http://x"; echo; echo "hello page"`)
	out := toolFetchPage(context.Background(), "http://x", "", 0, 0)
	if !strings.Contains(out, "hello page") || !strings.Contains(out, "Untrusted") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestFetchTruncatesAndOffsetContinues(t *testing.T) {
	fakePython(t, `printf 'abcdefghij'`)
	out := toolFetchPage(context.Background(), "http://x", "", 4, 0)
	if !strings.Contains(out, "abcd") || strings.Contains(out, "abcde") || !strings.Contains(out, "offset=4") {
		t.Fatalf("first chunk wrong: %s", out)
	}
	out = toolFetchPage(context.Background(), "http://x", "", 4, 4)
	if !strings.Contains(out, "efgh") || !strings.Contains(out, "offset=8") {
		t.Fatalf("second chunk wrong: %s", out)
	}
	if out = toolFetchPage(context.Background(), "http://x", "", 4, 99); !strings.Contains(out, "past the end") {
		t.Fatalf("offset past end: %s", out)
	}
}

func TestFetchKeepsMultibyteRunesWhole(t *testing.T) {
	fakePython(t, `printf 'héllo wörld'`)
	out := toolFetchPage(context.Background(), "http://x", "", 2, 0)
	if !strings.Contains(out, "hé") || strings.Contains(out, "�") {
		t.Fatalf("rune split: %q", out)
	}
}

func TestFetchReportsScriptError(t *testing.T) {
	fakePython(t, `echo "INFO: noise" >&2; echo "RuntimeError: HTTP 403" >&2; exit 1`)
	out := toolFetchPage(context.Background(), "http://x", "", 0, 0)
	if !strings.Contains(out, "HTTP 403") || strings.Contains(out, "noise") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestFetchMissingPython(t *testing.T) {
	t.Setenv("SCRAPLING_PYTHON", "/nonexistent/python")
	if out := toolFetchPage(context.Background(), "http://x", "", 0, 0); !strings.Contains(out, "not found") {
		t.Fatal(out)
	}
}

func TestFetchAlwaysAsksForApproval(t *testing.T) {
	action, reason := check("fetch_page", map[string]any{"url": "https://example.com/a"}, "/tmp")
	if action != "ask" || !strings.Contains(reason, "https://example.com/a") {
		t.Fatalf("got %s / %s", action, reason)
	}
}
