package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Which tool calls need a human.
//
// The sandbox decides what is *possible*. These rules only decide what is
// worth interrupting for - read-only commands run silently, risky ones stop
// and ask. Ported line-for-line from NeuralCode's permissions.py.

// Last matching rule wins, so the catch-all comes first.
var bashRuleOrder = []string{
	"*",
	"ls*", "pwd", "cd *", "echo *", "sort*", "uniq*", "cut *", "basename *",
	"dirname *", "date*", "env", "cat *", "head *", "tail *", "wc *", "file *",
	"which *", "grep *", "rg *", "find *", "tree*",
	"git status*", "git diff*", "git log*", "git show*", "git ls-files*",
	"pytest*", "python -m pytest*",
	"rm *", "sudo *", "chmod *", "chown *",
	"git push*", "git reset*", "git clean*",
}

var bashRules = map[string]string{
	"*": "ask",
	"ls*": "allow", "pwd": "allow", "cd *": "allow", "echo *": "allow",
	"sort*": "allow", "uniq*": "allow", "cut *": "allow", "basename *": "allow",
	"dirname *": "allow", "date*": "allow", "env": "allow", "cat *": "allow",
	"head *": "allow", "tail *": "allow", "wc *": "allow", "file *": "allow",
	"which *": "allow", "grep *": "allow", "rg *": "allow", "find *": "allow",
	"tree*": "allow",
	"git status*": "allow", "git diff*": "allow", "git log*": "allow",
	"git show*": "allow", "git ls-files*": "allow",
	"pytest*": "allow", "python -m pytest*": "allow",
	"rm *": "deny", "sudo *": "deny", "chmod *": "deny", "chown *": "deny",
	"git push*": "deny", "git reset*": "deny", "git clean*": "deny",
	// curl/wget/git-clone deliberately have NO entry here, unlike the denies
	// above - they fall through to the "*" catch-all (ask), giving internet
	// access (fetching repos, downloading files) the same approval gate as
	// every other risky command instead of being blocked outright.
}

// fnmatch mirrors Python's fnmatch semantics, where * matches any sequence
// of characters including '/'. filepath.Match refuses to cross separators,
// which would break patterns like "rm *" against "rm -rf /tmp/foo".
func fnmatch(pattern, s string) bool {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	return re.MatchString(s)
}

// splitCommand splits a compound command on the separators that actually
// separate. A naive split on | and ; also cuts inside quotes, so
// rg "cap|max" would break into fragments that match no rule and fall
// through to "ask". Anything quoted or backslash-escaped is an argument,
// not a separator.
func splitCommand(command string) []string {
	var parts []string
	var current []rune
	var quote rune
	runes := []rune(command)
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch {
		case quote != 0:
			current = append(current, c)
			if c == quote {
				quote = 0
			}
		case c == '\\':
			current = append(current, c)
			i++
			if i < len(runes) {
				current = append(current, runes[i])
			}
		case c == '"' || c == '\'':
			quote = c
			current = append(current, c)
		case c == '&' && len(current) > 0 && (current[len(current)-1] == '>' || current[len(current)-1] == '<'):
			// >&1, 2>&1, <&3: a descriptor redirection, not a command separator.
			current = append(current, c)
		case c == '&' || c == '|' || c == ';':
			parts = append(parts, string(current))
			current = nil
			for i+1 < len(runes) && (runes[i+1] == '&' || runes[i+1] == '|') {
				i++
			}
		default:
			current = append(current, c)
		}
		i++
	}
	parts = append(parts, string(current))

	var result []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// fdDup matches the target of a file-descriptor duplication or close, as in
// 2>&1, >&2 and 2>&-. That redirects one stream onto another (or closes it);
// it never opens a file. A file target after >& (ls >& out.txt) does not match.
var fdDup = regexp.MustCompile(`^&([0-9]+|-)$`)

func isFDDup(target string) bool { return fdDup.MatchString(target) }

// hasHiddenEffects reports whether a command part can write files or run
// other commands in ways its leading word doesn't show: output redirection
// (echo x > file), command or process substitution (echo $(rm ...)), or
// find's -exec/-delete. Such a part never runs silently, even when it
// starts with an allowed command like echo or find.
func hasHiddenEffects(part string) bool {
	var quote rune
	runes := []rune(part)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch {
		case c == '\\' && quote != '\'':
			i++ // escaped character is literal
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '`' || (c == '$' && next == '('):
			return true // substitution runs even inside double quotes
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '>':
			rest := strings.TrimLeft(string(runes[i+1:]), "> ")
			target, _, _ := strings.Cut(rest, " ")
			if target != "/dev/null" && !isFDDup(target) {
				return true
			}
		case c == '<' && next == '(':
			return true
		}
	}
	if fnmatch("find *", part) {
		for _, f := range strings.Fields(part) {
			switch f {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprintf", "-fls":
				return true
			}
		}
	}
	return false
}

// decide rates every part of a compound command; the strictest verdict wins.
// The user's own rules (permissions.json) are checked after the built-in
// ones, so they win when both match.
func decide(command string) string {
	userRules := currentUserRules()
	var verdicts []string
	for _, part := range splitCommand(command) {
		action := "ask"
		for _, pattern := range bashRuleOrder {
			if fnmatch(pattern, part) {
				action = bashRules[pattern]
			}
		}
		for _, r := range userRules {
			if fnmatch(r.Pattern, part) {
				action = r.Action
			}
		}
		if action == "allow" && hasHiddenEffects(part) {
			action = "ask"
		}
		verdicts = append(verdicts, action)
	}
	for _, strictest := range []string{"deny", "ask"} {
		for _, v := range verdicts {
			if v == strictest {
				return strictest
			}
		}
	}
	return "allow"
}

func insideProject(path string, projectRoot string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs // path may not exist yet (e.g. a new file to write)
	}
	rel, err := filepath.Rel(projectRoot, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// check returns (action, reason). Action is "allow", "ask", or "deny".
func check(name string, args map[string]any, projectRoot string) (string, string) {
	switch name {
	case "bash":
		cmd, _ := args["command"].(string)
		action := decide(cmd)
		if action == "allow" && commandMentionsSecret(cmd) {
			return "ask", fmt.Sprintf("run (touches a secret file): %s", cmd)
		}
		return action, fmt.Sprintf("run: %s", cmd)
	case "tavily_search":
		q, _ := args["query"].(string)
		if looksLikeSecret(q) {
			return "ask", fmt.Sprintf("web search that looks like it contains a key or token: %s", q)
		}
		return "allow", fmt.Sprintf("web search: %s", q)
	case spawnAgentTool:
		// The subagent is read-only and its own commands are held to the same
		// rules with no way to ask, so starting one needs no approval.
		task, _ := args["task"].(string)
		if len(task) > 80 {
			task = task[:80] + "..."
		}
		return "allow", fmt.Sprintf("run a read-only subagent: %s", task)
	case "skill":
		// Read-only, and confined to installed skills' own folders.
		name, _ := args["name"].(string)
		return "allow", fmt.Sprintf("load skill: %s", name)
	case "fetch_page":
		// Sends a request from this machine to a URL the model chose, so it
		// always needs a human, and a URL carrying a key or token is called out.
		u, _ := args["url"].(string)
		if looksLikeSecret(u) {
			return "ask", fmt.Sprintf("fetch a page (URL looks like it contains a key or token): %s", u)
		}
		return "ask", fmt.Sprintf("fetch page: %s", u)
	case "read_file", "write_file", "str_replace":
		path, _ := args["path"].(string)
		if isSensitivePath(path, projectRoot) {
			return "deny", fmt.Sprintf("%s on a secret file (keys, tokens, .env): %s", name, path)
		}
		if !insideProject(resolvePath(path, projectRoot), projectRoot) {
			return "ask", fmt.Sprintf("%s outside %s: %s", name, projectRoot, path)
		}
	}
	return "allow", ""
}
