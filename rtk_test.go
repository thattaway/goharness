package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRTK puts a stand-in `rtk` first on PATH. Its `rewrite` subcommand
// rewrites exactly "echo plain" (exit 3, like the real one), fails on
// "echo boom", and reports "no equivalent" (exit 1) for anything else.
func fakeRTK(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "rewrite" ]; then
  case "$2" in
    "echo plain") echo "echo compressed"; exit 3;;
    "echo boom") echo "garbage that is not a command"; exit 2;;
    *) exit 1;;
  esac
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "rtk"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOHARNESS_RTK", "")
}

func bash(t *testing.T, args map[string]any) string {
	t.Helper()
	return runTool(context.Background(), t.TempDir(), "bash", args)
}

func TestBashRunsRewrittenCommandAndSaysSo(t *testing.T) {
	fakeRTK(t)
	out := bash(t, map[string]any{"command": "echo plain"})
	if !strings.Contains(out, "compressed") || strings.Contains(out, "plain\n") {
		t.Errorf("expected the rewritten command's output, got %q", out)
	}
	if !strings.Contains(out, "raw") {
		t.Errorf("compressed output should tell the model how to get the raw output: %q", out)
	}
}

func TestBashLeavesUnsupportedCommandsAlone(t *testing.T) {
	fakeRTK(t)
	out := bash(t, map[string]any{"command": "echo other"})
	if strings.TrimSpace(out) != "other" {
		t.Errorf("a command rtk has no rewrite for should run as written with no note: %q", out)
	}
}

func TestBashRawBypassesRTK(t *testing.T) {
	fakeRTK(t)
	out := bash(t, map[string]any{"command": "echo plain", "raw": true})
	if strings.TrimSpace(out) != "plain" {
		t.Errorf("raw=true should run the original command unmodified: %q", out)
	}
}

func TestBashRTKCanBeSwitchedOff(t *testing.T) {
	for _, v := range []string{"off", "0", "false"} {
		fakeRTK(t)
		t.Setenv("GOHARNESS_RTK", v)
		out := bash(t, map[string]any{"command": "echo plain"})
		if strings.TrimSpace(out) != "plain" {
			t.Errorf("GOHARNESS_RTK=%s: got %q", v, out)
		}
	}
}

func TestBashFallsBackWhenRewriteFails(t *testing.T) {
	fakeRTK(t)
	out := bash(t, map[string]any{"command": "echo boom"})
	if strings.TrimSpace(out) != "boom" {
		t.Errorf("a failing rewrite must fall back to the original command: %q", out)
	}
}

func TestBashWorksWithoutRTKInstalled(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("GOHARNESS_RTK", "")
	if out := bash(t, map[string]any{"command": "echo plain"}); strings.TrimSpace(out) != "plain" {
		t.Errorf("got %q", out)
	}
}

func TestBashToolSchemaOffersRaw(t *testing.T) {
	props := toolSchemas[0].Function.Parameters["properties"].(map[string]any)
	if _, ok := props["raw"]; !ok {
		t.Error("bash tool schema has no raw parameter")
	}
}
