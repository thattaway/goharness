package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Agent Skills (the open SKILL.md standard shared by Claude Code, Copilot,
// Amp, Hermes, OpenClaw...): a skill is a folder holding SKILL.md - YAML
// frontmatter with a name and description, then instructions - plus any
// files it refers to (chapters, scripts). Installing one means putting that
// folder in a skills directory; book-to-skill both is one and makes them.
//
// Skills load progressively so they don't eat the context window: the
// system prompt only lists each skill's name and description, the model
// loads a skill's instructions with the skill tool when a task calls for
// it, and pulls in the skill's other files (a book's chapters) the same
// way, one at a time. Typing /name args runs a skill directly.

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
	// Rank is the index of the root the skill came from (0 = the project's own);
	// the prompt lists lower ranks first, so the cap never drops your own skills
	// in favour of a bulk-installed set. Skip is why it can't run here (wrong
	// platform, missing command or environment variable); skipped skills are
	// hidden from the model and from /name.
	Rank int    `json:"-"`
	Skip string `json:"-"`
}

// Overridable in tests.
var (
	skillOS  = runtime.GOOS
	lookPath = exec.LookPath
	getenv   = os.Getenv
)

// skillInstructionsHeader starts every loaded SKILL.md, so history trimming
// can recognize and keep it.
const skillInstructionsHeader = "Skill: "

const (
	maxListedSkills  = 60
	promptSkillDesc  = 280  // longest description the system prompt carries per skill
	listSkillDesc    = 160  // and in the skill tool's own listing
	maxSkillDescLen  = 1024 // the Agent Skills spec's limit; the "use when" part matters
	skillChunkChars  = 12000
	maxSkillFileList = 200
)

// skillRoots are the folders searched for skills, highest priority first:
// the project's own, then goharness's, then the other agents' shared ones.
func skillRoots(projectRoot string) []string {
	home, _ := os.UserHomeDir()
	envOr := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	roots := []string{filepath.Join(projectRoot, ".goharness", "skills")}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".goharness", "skills"))
	}
	// only goharness's own folders unless GOHARNESS_ALL_SKILLS is set
	if os.Getenv("GOHARNESS_ALL_SKILLS") == "" {
		return roots
	}
	roots = append(roots,
		filepath.Join(projectRoot, ".agents", "skills"),
		filepath.Join(projectRoot, ".claude", "skills"),
	)
	if home != "" {
		roots = append(roots,
			filepath.Join(home, ".agents", "skills"), // the cross-agent default book-to-skill installs to
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".copilot", "skills"),
			filepath.Join(envOr("HERMES_HOME", filepath.Join(home, ".hermes")), "skills"),
			filepath.Join(envOr("OPENCLAW_STATE_DIR", filepath.Join(home, ".openclaw")), "skills"),
		)
	}
	return roots
}

// discoverSkills finds every installed skill. A name found in an earlier
// root hides the same name in a later one, so a project can override.
func discoverSkills(projectRoot string) []Skill {
	out, _ := scanSkills(projectRoot)
	return out
}

// scanSkills returns the skills that can run on this machine, and those hidden
// because they can't (with the reason in Skip).
func scanSkills(projectRoot string) (out, hidden []Skill) {
	seen := map[string]bool{}
	rank := 0
	add := func(dir string) bool {
		sk, ok := readSkill(dir)
		if !ok {
			return false
		}
		sk.Rank = rank
		if sk.Skip != "" {
			hidden = append(hidden, sk)
			return true
		}
		if !seen[sk.Name] {
			seen[sk.Name] = true
			out = append(out, sk)
		}
		return true
	}
	for i, root := range skillRoots(projectRoot) {
		rank = i
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if add(dir) {
				continue
			}
			// Hermes groups skills by category: skills/<category>/<slug>/.
			if sub, err := os.ReadDir(dir); err == nil {
				for _, se := range sub {
					if !strings.HasPrefix(se.Name(), ".") {
						add(filepath.Join(dir, se.Name()))
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	// A copy that can't run isn't news when another copy with the same name can.
	kept := hidden[:0]
	for _, h := range hidden {
		if !seen[h.Name] {
			kept = append(kept, h)
		}
	}
	return out, kept
}

var (
	listField = regexp.MustCompile(`(?i)\b(commands|env_vars)\s*:\s*\[([^\]]*)\]`)
	osNames   = map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}
)

func listItems(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// skillSkipReason says why a skill can't run here, from its frontmatter:
// "platforms: [linux, macos]" and "prerequisites:" with "commands: [gh]" and
// "env_vars: [KEY]" (the Hermes convention). Empty means it can.
func skillSkipReason(meta map[string]string) string {
	if p := strings.TrimSpace(meta["platforms"]); p != "" {
		have := osNames[skillOS]
		if have == "" {
			have = skillOS
		}
		ok := false
		list := listItems(strings.Trim(strings.SplitN(p, "#", 2)[0], "[] "))
		for _, x := range list {
			if strings.EqualFold(x, have) {
				ok = true
			}
		}
		if len(list) > 0 && !ok {
			return "only for " + strings.Join(list, ", ")
		}
	}
	for _, m := range listField.FindAllStringSubmatch(meta["prerequisites"], -1) {
		for _, item := range listItems(m[2]) {
			if strings.EqualFold(m[1], "commands") {
				if _, err := lookPath(item); err != nil {
					return "needs the command " + item
				}
			} else if getenv(item) == "" {
				return "needs the environment variable " + item
			}
		}
	}
	return ""
}

func findSkill(projectRoot, name string) (Skill, bool) {
	for _, s := range discoverSkills(projectRoot) {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// readSkill reads dir/SKILL.md (following symlinks, which skill installers
// use) and returns the skill it describes.
func readSkill(dir string) (Skill, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, false
	}
	meta, body := splitFrontmatter(string(data))
	sk := Skill{Name: meta["name"], Description: meta["description"], Dir: dir, Skip: skillSkipReason(meta)}
	if sk.Name == "" {
		sk.Name = filepath.Base(dir)
	}
	sk.Name = strings.ToLower(strings.TrimSpace(sk.Name))
	if sk.Description == "" {
		sk.Description = firstParagraph(body)
	}
	sk.Description = strings.Join(strings.Fields(sk.Description), " ")
	if r := []rune(sk.Description); len(r) > maxSkillDescLen {
		sk.Description = string(r[:maxSkillDescLen]) + "…"
	}
	return sk, true
}

// splitFrontmatter separates a leading "---" YAML block from the body. It
// understands the small part of YAML skills use: key: value, quoted values,
// indented continuation lines, and ">" / "|" block scalars.
func splitFrontmatter(doc string) (map[string]string, string) {
	meta := map[string]string{}
	doc = strings.TrimPrefix(doc, "\uFEFF")
	if !strings.HasPrefix(doc, "---") {
		return meta, doc
	}
	lines := strings.Split(doc, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r ") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, doc
	}
	var key string
	for _, raw := range lines[1:end] {
		line := strings.TrimRight(raw, "\r")
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		if indented && key != "" {
			meta[key] = strings.TrimSpace(meta[key] + " " + strings.TrimSpace(line))
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			key = ""
			continue
		}
		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch v {
		case ">", "|", ">-", "|-", ">+", "|+":
			v = ""
		}
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		meta[key] = v
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

func firstParagraph(body string) string {
	for _, block := range strings.Split(body, "\n\n") {
		b := strings.TrimSpace(block)
		if b != "" && !strings.HasPrefix(b, "#") {
			return b
		}
	}
	return ""
}

// skillPromptSection is the skills part of the system prompt: names and
// descriptions only.
func skillPromptSection(projectRoot string) string {
	skills := discoverSkills(projectRoot)
	if len(skills) == 0 {
		return ""
	}
	// Your own skills first, then each later root; within a root, by name. The
	// cap below then drops the bulk-installed ones, not the ones you wrote.
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Rank < skills[j].Rank })
	var b strings.Builder
	b.WriteString("SKILLS. These skills are installed. When a request matches one, call the skill tool with its name " +
		"to load its instructions before you start, then follow them. Load the other files a skill mentions (chapters, " +
		"references) with the skill tool's file argument, only when you need them. A skill's scripts live in its " +
		"directory: run them by absolute path. When the user writes /<skill-name>, they are asking for that skill.\n")
	for i, s := range skills {
		if i == maxListedSkills {
			fmt.Fprintf(&b, "- … and %d more not listed here: call the skill tool with no name to list them all\n", len(skills)-i)
			break
		}
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, shortenSkillText(s.Description, promptSkillDesc))
	}
	return strings.TrimRight(b.String(), "\n")
}

// shortenSkillText cuts s to at most n characters, at a word boundary, with an ellipsis.
func shortenSkillText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.-") + "…"
}

// listSkills is what the skill tool returns when called with no name: every
// installed skill with a short description, in the same order as the prompt.
func listSkills(projectRoot string, offset int) string {
	skills, hidden := scanSkills(projectRoot)
	if len(skills) == 0 && len(hidden) == 0 {
		return "No skills are installed."
	}
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Rank < skills[j].Rank })
	var b strings.Builder
	fmt.Fprintf(&b, "Installed skills (%d). Load one with the skill tool's name argument:\n", len(skills))
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, shortenSkillText(s.Description, listSkillDesc))
	}
	if len(hidden) > 0 {
		sort.Slice(hidden, func(i, j int) bool { return hidden[i].Name < hidden[j].Name })
		b.WriteString("\nNot available on this machine:\n")
		for _, s := range hidden {
			fmt.Fprintf(&b, "- %s (%s)\n", s.Name, s.Skip)
		}
	}
	return chunk(strings.TrimRight(b.String(), "\n"), offset, "skill tool with no name")
}

// toolSkill loads a skill's instructions, or one of its files, in chunks.
func toolSkill(projectRoot string, args map[string]any) string {
	name, _ := args["name"].(string)
	file, _ := args["file"].(string)
	offset := 0
	if f, ok := args["offset"].(float64); ok && f > 0 {
		offset = int(f)
	}
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "/")))
	if name == "" {
		return listSkills(projectRoot, offset)
	}
	sk, ok := findSkill(projectRoot, name)
	if !ok {
		if _, hidden := scanSkills(projectRoot); len(hidden) > 0 {
			for _, h := range hidden {
				if h.Name == name {
					return fmt.Sprintf("Error: the skill %q can't run on this machine (%s).", name, h.Skip)
				}
			}
		}
		var names []string
		for _, s := range discoverSkills(projectRoot) {
			names = append(names, s.Name)
		}
		if len(names) == 0 {
			return fmt.Sprintf("Error: no skill named %q, and no skills are installed.", name)
		}
		return fmt.Sprintf("Error: no skill named %q. Installed skills: %s", name, strings.Join(names, ", "))
	}

	if file == "" || file == "SKILL.md" {
		text, err := skillInstructions(sk)
		if err != nil {
			return "Error: " + err.Error()
		}
		return chunk(text, offset, fmt.Sprintf("skill tool with name=%q", sk.Name))
	}

	path, err := skillFilePath(sk, file)
	if err != nil {
		return "Error: " + err.Error()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	return chunk(string(data), offset, fmt.Sprintf("skill tool with name=%q file=%q", sk.Name, file))
}

// skillInstructions is SKILL.md's body, introduced by where the skill lives
// and which files it has, so relative paths in it can be followed.
func skillInstructions(sk Skill) (string, error) {
	data, err := os.ReadFile(filepath.Join(sk.Dir, "SKILL.md"))
	if err != nil {
		return "", err
	}
	_, body := splitFrontmatter(string(data))
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\nBase directory for this skill: %s\n", skillInstructionsHeader, sk.Name, sk.Dir)
	b.WriteString("Paths in these instructions are relative to that directory. Load its files with the skill tool's file argument; run its scripts by absolute path.\n")
	if files := skillFiles(sk.Dir); len(files) > 0 {
		b.WriteString("Files in this skill: " + strings.Join(files, ", ") + "\n")
	}
	b.WriteString("\n" + strings.TrimSpace(body) + "\n")
	return b.String(), nil
}

func skillFiles(dir string) []string {
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(files) >= maxSkillFileList {
			return filepath.SkipDir
		}
		if d.IsDir() && p != dir && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() != "SKILL.md" {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files
}

// skillFilePath resolves a file inside a skill, refusing anything that
// escapes the skill's folder (.., absolute paths, symlinks out).
func skillFilePath(sk Skill, file string) (string, error) {
	if filepath.IsAbs(file) {
		return "", fmt.Errorf("give a path relative to the skill's folder, like chapters/ch01.md")
	}
	base, err := filepath.EvalSymlinks(sk.Dir)
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, filepath.FromSlash(file))
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("no file %q in skill %s (its files: %s)", file, sk.Name, strings.Join(skillFiles(sk.Dir), ", "))
	}
	if !within(base, real) {
		return "", fmt.Errorf("%q is outside the skill's folder", file)
	}
	return real, nil
}

// chunk returns text from offset, at most skillChunkChars, with a note on
// how to get the rest.
func chunk(text string, offset int, how string) string {
	if offset >= len(text) {
		return "(end of file)"
	}
	end := min(len(text), offset+skillChunkChars)
	// Don't split a UTF-8 character.
	for end < len(text) && end > offset && !isRuneStart(text[end]) {
		end--
	}
	out := text[offset:end]
	if end < len(text) {
		out += fmt.Sprintf("\n\n… (%d more characters: call the %s offset=%d to continue)", len(text)-end, how, end)
	}
	return out
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

var skillInvocation = regexp.MustCompile(`^/([A-Za-z0-9][A-Za-z0-9._-]*)(?:\s+([\s\S]*))?$`)

// parseSkillInvocation recognizes "/name args" when name is an installed
// skill; anything else (like "/usr/bin is a folder") is ordinary text.
func parseSkillInvocation(projectRoot, text string) (Skill, string, bool) {
	m := skillInvocation.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return Skill{}, "", false
	}
	sk, ok := findSkill(projectRoot, strings.ToLower(m[1]))
	return sk, strings.TrimSpace(m[2]), ok
}
