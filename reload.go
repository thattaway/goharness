package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reloading configuration without restarting. goharness reads four things
// from outside the process: the env file, the permission rules, each model
// server's /models listing (context lengths, vision support), and skills.
// Skills are already re-discovered on every message, so a reload refreshes the
// other three.

// envFromFile records the variables applyEnvFile set, with the value it set,
// so a reload can update or remove them without ever touching a variable that
// came from the real environment.
var (
	envMu       sync.Mutex
	envFromFile = map[string]string{}
)

func parseEnvFile() map[string]string {
	out := map[string]string{}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "goharness", "env"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// applyEnvFile loads ~/.config/goharness/env into the process environment and
// returns the keys it changed. A variable already set in the real environment
// is left alone; one this function set earlier is updated, or unset if its
// line has been removed.
func applyEnvFile() []string {
	envMu.Lock()
	defer envMu.Unlock()
	parsed := parseEnvFile()
	var changed []string
	for k, v := range parsed {
		cur, isSet := os.LookupEnv(k)
		prev, ours := envFromFile[k]
		if (!isSet || cur == "") || (ours && cur == prev) {
			if cur != v {
				os.Setenv(k, v)
				changed = append(changed, k)
			}
			envFromFile[k] = v
		}
	}
	for k, prev := range envFromFile {
		if _, still := parsed[k]; still {
			continue
		}
		if os.Getenv(k) == prev {
			os.Unsetenv(k)
			changed = append(changed, k)
		}
		delete(envFromFile, k)
	}
	sort.Strings(changed)
	return changed
}

// clearModelInfoCaches forgets the cached /models listings so the next
// request re-fetches context lengths and vision support - needed after the
// model server's own config changes.
func clearModelInfoCaches() {
	contextLengths.Lock()
	contextLengths.byURL = map[string]map[string]int{}
	contextLengths.at = map[string]time.Time{}
	contextLengths.Unlock()
	visionModels.Lock()
	visionModels.byURL = map[string]map[string]bool{}
	visionModels.at = map[string]time.Time{}
	visionModels.Unlock()
}

// reloadConfig re-reads configuration and returns a short, human-readable
// summary. It names changed env keys but never their values. A permission
// rules file that fails to parse is reported and the previous rules stay in
// force rather than being dropped.
func reloadConfig(projectRoot string) string {
	var lines []string

	if changed := applyEnvFile(); len(changed) == 0 {
		lines = append(lines, "env file: no changes")
	} else {
		lines = append(lines, "env file: updated "+strings.Join(changed, ", "))
	}

	if err := loadCustomProviders(); err != nil {
		lines = append(lines, fmt.Sprintf("custom providers: NOT reloaded (%v); keeping the previous list", err))
	} else {
		lines = append(lines, fmt.Sprintf("custom providers: %d loaded", len(listCustomProviders())))
	}

	if err := loadUserRules(); err != nil {
		lines = append(lines, fmt.Sprintf("permission rules: NOT reloaded (%v); keeping the previous rules", err))
	} else {
		n := len(currentUserRules())
		lines = append(lines, fmt.Sprintf("permission rules: %d rule%s loaded", n, plural(n)))
	}

	clearModelInfoCaches()
	lines = append(lines, "model info: will be re-fetched on the next request")

	n := len(discoverSkills(projectRoot))
	lines = append(lines, fmt.Sprintf("skills: %d installed", n))
	return strings.Join(lines, "\n")
}
