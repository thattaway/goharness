package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxToolChars = 8000 // crude context-window guard, ported loosely from NeuralCode's history.cap
)

// bashTimeout is a var so tests can shorten it.
var bashTimeout = 120 * time.Second

func capOutput(s string) string {
	if len(s) <= maxToolChars {
		return s
	}
	return s[:maxToolChars] + fmt.Sprintf("\n... (truncated, %d more chars)", len(s)-maxToolChars)
}

// runTool executes one tool call by name. Relative paths and bash commands
// resolve against root, the session's project directory. ctx cancels a
// running bash command; the other tools are quick enough not to need it.
func runTool(ctx context.Context, root, name string, args map[string]any) string {
	str := func(k string) string { v, _ := args[k].(string); return v }
	path := func() string { return resolvePath(str("path"), root) }
	switch name {
	case "bash":
		if raw, _ := args["raw"].(bool); raw {
			return toolBash(ctx, root, str("command"))
		}
		return toolBashCompressed(ctx, root, str("command"))
	case "read_file":
		return toolReadFile(path())
	case "write_file":
		return toolWriteFile(path(), str("content"))
	case "str_replace":
		allowMulti, _ := args["allow_multi_edit"].(bool)
		return toolStrReplace(path(), str("old_str"), str("new_str"), allowMulti)
	case "tavily_search":
		n, _ := args["max_results"].(float64)
		return toolTavilySearch(str("query"), int(n), str("search_depth"))
	case "skill":
		return toolSkill(root, args)
	case "fetch_page":
		n, _ := args["max_chars"].(float64)
		off, _ := args["offset"].(float64)
		return toolFetchPage(ctx, str("url"), str("mode"), int(n), int(off))
	}
	return fmt.Sprintf("Error: no tool named '%s'", name)
}

func toolBash(parent context.Context, dir, command string) string {
	ctx, cancel := context.WithTimeout(parent, bashTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	setProcessGroup(cmd)
	// Backstop: if something still holds the pipe after the kill, give up on
	// it after a moment rather than waiting for it forever.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if parent.Err() != nil {
		return "Cancelled by the user."
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Timed out after %s and was killed. Narrow it down.", bashTimeout)
	}
	result := string(out)
	if result == "" {
		if err != nil {
			result = err.Error()
		} else {
			result = "(no output)"
		}
	}
	return capOutput(result)
}

func toolReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	return capOutput(string(data))
}

func toolWriteFile(path, content string) string {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	return fmt.Sprintf("Wrote %s", path)
}

// toolStrReplace replaces old_str, which must appear exactly once unless
// allowMulti is set - replacing the first of several matches would quietly
// edit the wrong place.
func toolStrReplace(path, oldStr, newStr string, allowMulti bool) string {
	if oldStr == "" {
		return "Error: 'old_str' must not be empty."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error reading file: %s", err)
	}
	content := string(data)

	n := strings.Count(content, oldStr)
	switch {
	case n == 0:
		return "Error: 'old_str' not found in file. Re-read the file and copy the text exactly, including whitespace."
	case n > 1 && !allowMulti:
		return fmt.Sprintf("Error: 'old_str' appears %d times. Include more surrounding lines so it matches exactly once, or set allow_multi_edit to replace every match.", n)
	}
	content = strings.ReplaceAll(content, oldStr, newStr)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("Error writing file: %s", err)
	}
	if n > 1 {
		return fmt.Sprintf("Successfully replaced %d matches in %s", n, path)
	}
	return fmt.Sprintf("Successfully replaced text in %s", path)
}
