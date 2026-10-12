package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates <dir>/<name>/SKILL.md (plus extra files) and returns the skill dir.
func writeSkill(t *testing.T, dir, name, skillMD string, extra map[string]string) string {
	t.Helper()
	sd := filepath.Join(dir, name)
	for rel, content := range extra {
		p := filepath.Join(sd, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	os.MkdirAll(sd, 0o755)
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return sd
}

const bookSkill = `---
name: book-to-skill
description: >
  Turn any technical book or document folder
  into an agent skill.
license: MIT
---

# Book to skill

Run ` + "`python3 scripts/extract.py <path>`" + ` from this skill's directory.
`

func TestDiscoverSkills(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	// The cross-agent root, a Claude Code root, a Hermes-style category folder,
	// and a project-local skill that shadows a user one.
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "book-to-skill", bookSkill, map[string]string{"scripts/extract.py": "print(1)"})
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "ddia", "---\nname: ddia\ndescription: \"Designing Data-Intensive Applications\"\n---\nbody", nil)
	writeSkill(t, filepath.Join(home, ".hermes", "skills", "books"), "sre", "---\nname: sre\ndescription: Site reliability\n---\n", nil)
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "shadowed", "---\nname: shadowed\ndescription: user copy\n---\n", nil)
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "shadowed", "---\nname: shadowed\ndescription: project copy\n---\n", nil)
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "no-frontmatter", "# Title\n\nDoes a useful thing.\n", nil)
	os.MkdirAll(filepath.Join(home, ".agents", "skills", "not-a-skill"), 0o755)
	t.Cleanup(func() {
		for _, d := range []string{".agents", ".claude", ".hermes"} {
			os.RemoveAll(filepath.Join(home, d))
		}
	})

	skills := discoverSkills(project)
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if len(skills) != 5 {
		t.Fatalf("skills = %+v", skills)
	}
	if d := byName["book-to-skill"].Description; d != "Turn any technical book or document folder into an agent skill." {
		t.Errorf("folded description = %q", d)
	}
	if d := byName["ddia"].Description; d != "Designing Data-Intensive Applications" {
		t.Errorf("quoted description = %q", d)
	}
	if byName["shadowed"].Description != "project copy" {
		t.Errorf("project skill should win: %+v", byName["shadowed"])
	}
	if d := byName["no-frontmatter"].Description; d != "Does a useful thing." {
		t.Errorf("fallback description = %q", d)
	}
	if _, ok := byName["sre"]; !ok {
		t.Error("category-nested skill not found")
	}

	// The system prompt lists them, without their bodies.
	sys := systemPrompt(project, Settings{Model: "m"})
	if !strings.Contains(sys, "book-to-skill: Turn any technical book") || strings.Contains(sys, "scripts/extract.py") {
		t.Fatalf("system prompt skill section wrong:\n%s", sys)
	}
}

func TestSkillTool(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".agents", "skills")
	dir := writeSkill(t, root, "book-to-skill", bookSkill, map[string]string{
		"chapters/ch01-intro.md": "Chapter one text",
		"glossary.md":            strings.Repeat("term ", 5000),
	})
	os.WriteFile(filepath.Join(home, "secret.txt"), []byte("nope"), 0o644)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".agents")) })
	project := t.TempDir()
	ctx := context.Background()

	out := runTool(ctx, project, "skill", map[string]any{"name": "book-to-skill"})
	if !strings.Contains(out, "Base directory for this skill: "+dir) || !strings.Contains(out, "# Book to skill") ||
		!strings.Contains(out, "chapters/ch01-intro.md") || strings.Contains(out, "license: MIT") {
		t.Fatalf("loading the skill:\n%s", out)
	}
	if out := runTool(ctx, project, "skill", map[string]any{"name": "book-to-skill", "file": "chapters/ch01-intro.md"}); !strings.Contains(out, "Chapter one text") {
		t.Fatalf("loading a chapter: %s", out)
	}
	// Long files come in chunks.
	first := runTool(ctx, project, "skill", map[string]any{"name": "book-to-skill", "file": "glossary.md"})
	if !strings.Contains(first, "offset=") {
		t.Fatalf("no continuation hint in a long file: %s", first[len(first)-200:])
	}
	// Nothing outside the skill's folder.
	for _, bad := range []string{"../../../secret.txt", "/etc/passwd", "chapters/../../../../secret.txt"} {
		if out := runTool(ctx, project, "skill", map[string]any{"name": "book-to-skill", "file": bad}); !strings.HasPrefix(out, "Error") {
			t.Errorf("file %q was readable: %s", bad, out)
		}
	}
	if out := runTool(ctx, project, "skill", map[string]any{"name": "nope"}); !strings.Contains(out, "book-to-skill") {
		t.Errorf("unknown skill should list the available ones: %s", out)
	}
	if action, _ := check("skill", map[string]any{"name": "book-to-skill"}, project); action != "allow" {
		t.Errorf("skill tool needs approval: %s", action)
	}
}

func TestSlashInvokesSkill(t *testing.T) {
	home, _ := os.UserHomeDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "book-to-skill", bookSkill, nil)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".agents")) })

	llm, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "converting"} })
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "/book-to-skill ~/books/ddia.pdf ddia", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	// The model gets the skill's instructions as if it had loaded them itself,
	// and the user's message stays as typed.
	sent := llm.request(0)["messages"].([]any)
	var sawUser, sawResult bool
	for _, m := range sent {
		mm := m.(map[string]any)
		content, _ := mm["content"].(string)
		if mm["role"] == "user" && content == "/book-to-skill ~/books/ddia.pdf ddia" {
			sawUser = true
		}
		if mm["role"] == "tool" && strings.Contains(content, "# Book to skill") && strings.Contains(content, "~/books/ddia.pdf ddia") {
			sawResult = true
		}
	}
	if !sawUser || !sawResult {
		t.Fatalf("messages sent = %v", sent)
	}
	if calls := c.find(EvToolCall); len(calls) != 1 || calls[0].Name != "skill" {
		t.Fatalf("tool_call events = %+v", calls)
	}
	// An unknown /word is just text.
	s2, _ := newTestSession(t, url)
	s2.Run(context.Background(), "/usr/bin is a folder", nil, func(Event) {}, noApprover(t))
	for _, m := range s2.Messages() {
		if m.Role == "tool" {
			t.Fatal("an unknown slash word was treated as a skill")
		}
	}
}

func TestPromptListsYourOwnSkillsFirstAndPointsToTheFullList(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	// 70 bulk-installed skills whose names sort before the project's own, which the old alphabetical cap would have kept instead.
	for i := 0; i < 70; i++ {
		n := fmt.Sprintf("aaa-bulk-%02d", i)
		writeSkill(t, filepath.Join(home, ".hermes", "skills"), n, "---\nname: "+n+"\ndescription: bulk "+n+"\n---\nbody", nil)
	}
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "zzz-mine", "---\nname: zzz-mine\ndescription: written by me\n---\nbody", nil)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".hermes")) })

	prompt := skillPromptSection(project)
	if !strings.Contains(prompt, "- zzz-mine: written by me") {
		t.Fatalf("the project's own skill was cut from the list:\n%s", prompt)
	}
	if strings.Index(prompt, "zzz-mine") > strings.Index(prompt, "aaa-bulk-00") {
		t.Errorf("own skills should be listed before bulk ones")
	}
	if got := strings.Count(prompt, "\n- ") - 1; got != maxListedSkills { // minus the "… and N more" line
		t.Errorf("listed %d skills, want %d", got, maxListedSkills)
	}
	if !strings.Contains(prompt, "and 11 more not listed here: call the skill tool with no name") {
		t.Errorf("no pointer to the full list:\n%s", prompt[len(prompt)-300:])
	}
}

func TestSkillToolWithNoNameListsEverySkill(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	for i := 0; i < 70; i++ {
		n := fmt.Sprintf("bulk-%02d", i)
		writeSkill(t, filepath.Join(home, ".hermes", "skills"), n, "---\nname: "+n+"\ndescription: "+strings.Repeat("long ", 80)+"\n---\nbody", nil)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".hermes")) })
	out := toolSkill(project, map[string]any{})
	if !strings.Contains(out, "Installed skills (70)") || !strings.Contains(out, "bulk-69") && !strings.Contains(out, "offset=") {
		t.Fatalf("listing:\n%s", out[:300])
	}
	if strings.Contains(out, strings.Repeat("long ", 70)) {
		t.Errorf("descriptions in the listing should be shortened")
	}
}

func TestSkillsThatCannotRunHereAreHiddenWithAReason(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	old := [3]any{skillOS, lookPath, getenv}
	skillOS = "linux"
	lookPath = func(name string) (string, error) {
		if name == "curl" {
			return "/usr/bin/curl", nil
		}
		return "", fmt.Errorf("not found")
	}
	getenv = func(k string) string {
		if k == "HAVE_KEY" {
			return "x"
		}
		return ""
	}
	t.Cleanup(func() {
		skillOS, lookPath, getenv = old[0].(string), old[1].(func(string) (string, error)), old[2].(func(string) string)
	})
	root := filepath.Join(home, ".hermes", "skills")
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".hermes")) })
	mk := func(name, front string) {
		writeSkill(t, root, name, "---\nname: "+name+"\ndescription: d "+name+"\n"+front+"---\nbody", nil)
	}
	mk("plain", "")
	mk("any-os", "platforms: [linux, macos, windows]   # audit\n")
	mk("mac-only", "platforms: [macos]\n")
	mk("has-curl", "prerequisites:\n  commands: [curl]\n")
	mk("needs-gh", "prerequisites:\n  commands: [curl, gh]\n")
	mk("has-key", "prerequisites:\n  env_vars: [HAVE_KEY]\n  commands: [curl]\n")
	mk("needs-key", "prerequisites:\n  env_vars: [NOPE_KEY]\n")
	// A copy in a later root that can't run, shadowed by a working copy in an earlier one, is not reported.
	writeSkill(t, filepath.Join(home, ".goharness", "skills"), "dup", "---\nname: dup\ndescription: works\n---\nbody", nil)
	mk("dup", "prerequisites:\n  commands: [gh]\n")
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".goharness")) })

	var names []string
	for _, s := range discoverSkills(project) {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "any-os,dup,has-curl,has-key,plain" {
		t.Fatalf("available skills = %s", got)
	}
	if strings.Contains(skillPromptSection(project), "mac-only") {
		t.Errorf("a macOS-only skill was offered on linux")
	}
	list := toolSkill(project, map[string]any{})
	for _, want := range []string{"mac-only (only for macos)", "needs-gh (needs the command gh)", "needs-key (needs the environment variable NOPE_KEY)"} {
		if !strings.Contains(list, want) {
			t.Errorf("listing lacks %q:\n%s", want, list)
		}
	}
	if strings.Contains(list, "dup (") {
		t.Errorf("a shadowed, unusable copy was reported as unavailable:\n%s", list)
	}
	if out := toolSkill(project, map[string]any{"name": "mac-only"}); !strings.Contains(out, "can't run on this machine (only for macos)") {
		t.Errorf("loading a hidden skill by name: %s", out)
	}
	if _, _, ok := parseSkillInvocation(project, "/mac-only do it"); ok {
		t.Errorf("/mac-only should not run a hidden skill")
	}
	skillOS = "darwin"
	if _, ok := findSkill(project, "mac-only"); !ok {
		t.Errorf("mac-only should be available on macOS")
	}
}

func TestSystemPromptCanOmitTheSkillsList(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "some-skill", "---\nname: some-skill\ndescription: does things\n---\nbody", nil)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".agents")) })
	if !strings.Contains(systemPrompt(project, Settings{}), "- some-skill: does things") {
		t.Fatal("the normal prompt should list skills")
	}
	if strings.Contains(systemPrompt(project, Settings{NoSkills: true}), "some-skill") {
		t.Error("NoSkills should leave the list out")
	}
}

func init() { os.Setenv("GOHARNESS_ALL_SKILLS", "1") } // the other tests exercise every skill folder

func TestSkillRootsAreGoharnessOnlyByDefault(t *testing.T) {
	t.Setenv("GOHARNESS_ALL_SKILLS", "")
	roots := skillRoots("/p")
	if len(roots) != 2 || !strings.HasSuffix(roots[0], "/p/.goharness/skills") || !strings.HasSuffix(roots[1], "/.goharness/skills") {
		t.Errorf("roots = %v", roots)
	}
}
