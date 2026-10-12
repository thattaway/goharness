package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Guards against the agent reading secrets and sending them somewhere.
// read_file and tavily_search run without approval, so without these
// checks a prompt injection could chain "read ~/.ssh/id_ed25519" into
// "search for <key>" and leak it to a third party with nobody asked.

// Directories whose contents are secret wherever they appear in a path.
var sensitiveDirs = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true,
	".docker": true, ".password-store": true,
}

// Directories under ~/.config that hold tokens.
var sensitiveConfigDirs = map[string]bool{"goharness": true, "gh": true, "gcloud": true}

// File names that are secret anywhere.
var sensitiveNames = map[string]bool{
	".netrc": true, ".git-credentials": true, ".pgpass": true, ".npmrc": true, ".pypirc": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true, "credentials": true,
}

var sensitiveExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".keystore": true}

// resolvePath expands a leading ~ and makes a relative path absolute
// against the project root, the way the tools will use it.
func resolvePath(p, root string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// isSensitivePath reports whether a file path (after resolving symlinks,
// so a link can't disguise a key file) points at a secret.
func isSensitivePath(p, root string) bool {
	abs := resolvePath(p, root)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && sensitivePathText(resolved) {
		return true
	}
	return sensitivePathText(abs)
}

// sensitivePathText checks a path by its text alone.
func sensitivePathText(p string) bool {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i, part := range parts {
		if sensitiveDirs[part] && i < len(parts)-1 {
			return true
		}
		if part == ".config" && i+1 < len(parts) && sensitiveConfigDirs[parts[i+1]] {
			return true
		}
	}
	name := parts[len(parts)-1]
	if sensitiveNames[name] || name == "web_token" || sensitiveDirs[name] {
		return true
	}
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return !strings.HasSuffix(name, ".example") && !strings.HasSuffix(name, ".sample") && !strings.HasSuffix(name, ".template")
	}
	return sensitiveExts[strings.ToLower(filepath.Ext(name))]
}

var shellWordSplit = regexp.MustCompile(`[\s"'=<>|;&()` + "`" + `]+`)

// commandMentionsSecret reports whether any word of a shell command looks
// like a path to a secret (cat ~/.ssh/id_rsa, grep KEY .env).
func commandMentionsSecret(command string) bool {
	for _, word := range shellWordSplit.Split(command, -1) {
		if word == "" || strings.HasPrefix(word, "-") {
			continue
		}
		if strings.HasPrefix(word, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				word = home + strings.TrimPrefix(word, "~")
			}
		}
		if sensitivePathText(strings.TrimSuffix(word, "/")) {
			return true
		}
	}
	return false
}

var (
	knownKeyPrefix = regexp.MustCompile(`(?:tvly-|sk-|sk_live_|rk_live_|pk_live_|ghp_|gho_|ghs_|github_pat_|glpat-|xox[abprs]-|hf_|AKIA|ASIA|AIza)[A-Za-z0-9_\-]{6,}`)
	longToken      = regexp.MustCompile(`[A-Za-z0-9+/_\-=]{32,}`)
	hasDigit       = regexp.MustCompile(`[0-9]`)
	hasLetter      = regexp.MustCompile(`[A-Za-z]`)
)

// looksLikeSecret reports whether text (a search query, say) seems to
// contain a credential: a known key format, a long random-looking token, or
// the value of an environment variable that holds one.
func looksLikeSecret(text string) bool {
	if knownKeyPrefix.MatchString(text) {
		return true
	}
	for _, tok := range longToken.FindAllString(text, -1) {
		if hasDigit.MatchString(tok) && hasLetter.MatchString(tok) {
			return true
		}
	}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(k)
		if len(v) >= 8 && (strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")) && strings.Contains(text, v) {
			return true
		}
	}
	return false
}
