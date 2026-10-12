package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reloadHome points HOME at a temp dir and restores the globals reload touches.
func reloadHome(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "goharness"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldRules := currentUserRules()
	oldFrom := envFromFile
	envFromFile = map[string]string{}
	t.Cleanup(func() {
		userRules.Lock()
		userRules.rules = oldRules
		userRules.Unlock()
		envFromFile = oldFrom
	})
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadPicksUpEnvFileChanges(t *testing.T) {
	home := reloadHome(t)
	envPath := filepath.Join(home, ".config", "goharness", "env")
	os.Unsetenv("RELOAD_T_A")
	os.Unsetenv("RELOAD_T_B")
	t.Cleanup(func() { os.Unsetenv("RELOAD_T_A"); os.Unsetenv("RELOAD_T_B") })

	writeFile(t, envPath, "RELOAD_T_A=one\nexport RELOAD_T_B=\"keep\"\n")
	loadEnvFile()
	if os.Getenv("RELOAD_T_A") != "one" || os.Getenv("RELOAD_T_B") != "keep" {
		t.Fatalf("startup load: A=%q B=%q", os.Getenv("RELOAD_T_A"), os.Getenv("RELOAD_T_B"))
	}

	writeFile(t, envPath, "RELOAD_T_A=two\n") // A edited, B removed
	summary := reloadConfig(t.TempDir())
	if got := os.Getenv("RELOAD_T_A"); got != "two" {
		t.Errorf("edited value not picked up: A=%q", got)
	}
	if got, set := os.LookupEnv("RELOAD_T_B"); set {
		t.Errorf("removed key still set: B=%q", got)
	}
	if !strings.Contains(summary, "RELOAD_T_A") || !strings.Contains(summary, "RELOAD_T_B") {
		t.Errorf("summary should name the changed keys: %s", summary)
	}
	if strings.Contains(summary, "two") || strings.Contains(summary, "keep") {
		t.Errorf("summary leaks a value: %s", summary)
	}
}

func TestReloadDoesNotOverrideRealEnvironment(t *testing.T) {
	home := reloadHome(t)
	t.Setenv("RELOAD_T_C", "from-shell")
	writeFile(t, filepath.Join(home, ".config", "goharness", "env"), "RELOAD_T_C=from-file\n")
	loadEnvFile()
	reloadConfig(t.TempDir())
	if got := os.Getenv("RELOAD_T_C"); got != "from-shell" {
		t.Errorf("a variable set in the real environment was overwritten: %q", got)
	}
}

func TestReloadAppliesRulesAndKeepsOldOnes(t *testing.T) {
	home := reloadHome(t)
	rulesFile := filepath.Join(home, ".config", "goharness", "permissions.json")

	writeFile(t, rulesFile, `{"rules":[{"pattern":"reloadtest*","action":"deny"}]}`)
	if s := reloadConfig(t.TempDir()); !strings.Contains(s, "1 rule") {
		t.Errorf("summary = %s", s)
	}
	if rules := currentUserRules(); len(rules) != 1 || rules[0].Pattern != "reloadtest*" {
		t.Fatalf("rules not applied: %+v", rules)
	}

	writeFile(t, rulesFile, `{"rules": [ this is not json`)
	s := reloadConfig(t.TempDir())
	if !strings.Contains(s, "NOT reloaded") {
		t.Errorf("a broken rules file should be reported: %s", s)
	}
	if rules := currentUserRules(); len(rules) != 1 || rules[0].Pattern != "reloadtest*" {
		t.Errorf("previous rules were lost after a bad reload: %+v", rules)
	}
}

func TestReloadClearsModelInfoCaches(t *testing.T) {
	reloadHome(t)
	contextLengths.Lock()
	contextLengths.byURL["http://x"] = map[string]int{"m": 1}
	contextLengths.at["http://x"] = contextLengths.at["http://y"]
	contextLengths.Unlock()
	visionModels.Lock()
	visionModels.byURL["http://x"] = map[string]bool{"m": true}
	visionModels.Unlock()

	reloadConfig(t.TempDir())

	contextLengths.Lock()
	n1 := len(contextLengths.byURL)
	contextLengths.Unlock()
	visionModels.Lock()
	n2 := len(visionModels.byURL)
	visionModels.Unlock()
	if n1 != 0 || n2 != 0 {
		t.Errorf("model info caches not cleared: context=%d vision=%d", n1, n2)
	}
}
