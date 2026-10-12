package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		cmd, want string
	}{
		// read-only commands still run silently
		{"ls -la", "allow"},
		{"cat main.go | grep func", "allow"},
		{`rg "cap|max" .`, "allow"},
		{"echo 'a > b'", "allow"}, // > inside single quotes is text
		{`echo "a > b"`, "allow"}, // and inside double quotes
		{"find . -name '*.go'", "allow"},
		{"git status && git diff", "allow"},
		{"grep -r foo . 2>/dev/null", "allow"}, // discarding output writes nothing
		{"ls >> /dev/null", "allow"},
		{"ls -R lib 2>&1", "allow"}, // duplicating stderr onto stdout writes no file
		{"find . -type f 2>&1 | head -100", "allow"},
		{"cd p && grep -rn foo lib 2>&1", "allow"},
		{"cat notes.txt >&2", "allow"},
		{"ls 2>&-", "allow"},

		// writes and hidden execution need approval
		{"echo hi > out.txt", "ask"},
		{"echo appended >> notes.txt", "ask"},
		{"cat a.txt>b.txt", "ask"},
		{"ls 2>errors.log", "ask"},
		{"ls >/dev/null > out.txt", "ask"},
		{"ls >/dev/nullfoo", "ask"},
		{"ls >& out.txt", "ask"},     // >& with a file name writes the file
		{"ls &> out.txt", "ask"},     // so does &>
		{"ls 2>&1 > out.txt", "ask"}, // the later real redirection still asks
		{"ls 2>&1 >out.txt", "ask"},
		{"ls >&1x", "ask"}, // not a plain descriptor number
		{"echo hi 2>&1 > /tmp/x", "ask"},
		{"echo $(touch x)", "ask"},
		{`echo "$(touch x)"`, "ask"}, // substitution runs inside double quotes
		{"echo `touch x`", "ask"},
		{"diff <(ls a) <(ls b)", "ask"},
		{"find . -name '*.tmp' -delete", "ask"},
		{"find . -exec touch {} ;", "ask"},
		{"touch x", "ask"},

		// deny rules still win
		{"rm -rf build", "deny"},
		{"echo $(rm -rf ~)", "ask"}, // the rm is hidden inside echo's argument; at least it asks
		{"ls > x; rm y", "deny"},
		{"rm -rf build 2>&1", "deny"}, // a harmless redirect doesn't soften a deny
	}
	for _, c := range cases {
		if got := decide(c.cmd); got != c.want {
			t.Errorf("decide(%q) = %s, want %s", c.cmd, got, c.want)
		}
	}
}

func TestCheckSecrets(t *testing.T) {
	root := t.TempDir()
	home, _ := os.UserHomeDir()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"read_file", map[string]any{"path": filepath.Join(root, "main.go")}, "allow"},
		{"read_file", map[string]any{"path": "/etc/hostname"}, "ask"}, // outside the project
		{"read_file", map[string]any{"path": filepath.Join(home, ".ssh", "id_ed25519")}, "deny"},
		{"read_file", map[string]any{"path": "~/.ssh/config"}, "deny"},
		{"read_file", map[string]any{"path": filepath.Join(home, ".config", "goharness", "env")}, "deny"},
		{"read_file", map[string]any{"path": filepath.Join(root, ".env")}, "deny"},
		{"read_file", map[string]any{"path": filepath.Join(root, ".env.local")}, "deny"},
		{"read_file", map[string]any{"path": filepath.Join(root, "certs", "server.key")}, "deny"},
		{"read_file", map[string]any{"path": filepath.Join(root, ".environment.md")}, "allow"},
		{"bash", map[string]any{"command": "cat ~/.ssh/id_rsa"}, "ask"},
		{"bash", map[string]any{"command": "grep TOKEN .env"}, "ask"},
		{"bash", map[string]any{"command": "cat ~/.config/goharness/web_token"}, "ask"},
		{"bash", map[string]any{"command": "cat README.md"}, "allow"},
		{"tavily_search", map[string]any{"query": "golang generics tutorial"}, "allow"},
		{"tavily_search", map[string]any{"query": "tvly-dev-abc123 usage"}, "ask"},
		{"tavily_search", map[string]any{"query": "69a15d2d56dbf0a01562584d92ad79160366f7e27c8eb758"}, "ask"},
		{"tavily_search", map[string]any{"query": "error sk-proj-AbCdEf1234567890XyZ"}, "ask"},
		{"write_file", map[string]any{"path": filepath.Join(home, ".ssh", "authorized_keys")}, "deny"},
	}
	for _, c := range cases {
		if got, _ := check(c.name, c.args, root); got != c.want {
			t.Errorf("check(%s, %v) = %s, want %s", c.name, c.args, got, c.want)
		}
	}
}

func TestUserRules(t *testing.T) {
	t.Cleanup(func() { userRules.Lock(); userRules.rules = nil; userRules.Unlock() })
	if decide("go test ./...") != "ask" {
		t.Fatal("go test should ask by default")
	}
	if _, err := saveUserRules([]Rule{{Pattern: "go test*", Action: "allow"}, {Pattern: "make deploy*", Action: "deny"}}); err != nil {
		t.Fatal(err)
	}
	if got := decide("go test ./..."); got != "allow" {
		t.Errorf("go test = %s after allow rule", got)
	}
	if got := decide("go test ./... > out.txt"); got != "ask" {
		t.Errorf("allow rule bypassed the redirect check: %s", got)
	}
	if got := decide("make deploy prod"); got != "deny" {
		t.Errorf("make deploy = %s", got)
	}
	// Rules persist and reload.
	userRules.Lock()
	userRules.rules = nil
	userRules.Unlock()
	if err := loadUserRules(); err != nil || decide("go test ./x") != "allow" {
		t.Fatalf("reload: %v", err)
	}
	for _, bad := range [][]Rule{{{Pattern: "", Action: "allow"}}, {{Pattern: "x", Action: "maybe"}}} {
		if _, err := saveUserRules(bad); err == nil {
			t.Errorf("accepted invalid rules %v", bad)
		}
	}
	// The agent can't edit the rules file.
	path, _ := rulesPath()
	if got, _ := check("write_file", map[string]any{"path": path}, t.TempDir()); got != "deny" {
		t.Errorf("write_file on permissions.json = %s", got)
	}
}
