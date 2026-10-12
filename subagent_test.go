package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func toolNames(req map[string]any) []string {
	var out []string
	tools, _ := req["tools"].([]any)
	for _, tool := range tools {
		out = append(out, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	return out
}

func hasTool(req map[string]any, name string) bool {
	for _, n := range toolNames(req) {
		if n == name {
			return true
		}
	}
	return false
}

// toolResults returns the content of every tool message in a request.
func toolResults(req map[string]any) []string {
	var out []string
	for _, m := range req["messages"].([]any) {
		msg := m.(map[string]any)
		if msg["role"] == "tool" {
			out = append(out, msg["content"].(string))
		}
	}
	return out
}

func TestSpawnAgentReturnsSubagentAnswer(t *testing.T) {
	// Requests arrive in order: parent, child, parent again.
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{calls: []FunctionCall{toolCall(spawnAgentTool, map[string]any{"task": "find the config parser"})}}
		case 2:
			return fakeReply{text: "the parser is in config.go"}
		}
		return fakeReply{text: "all done"}
	})
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "investigate", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}

	if !hasTool(llm.request(0), spawnAgentTool) {
		t.Errorf("parent was not offered %s: %v", spawnAgentTool, toolNames(llm.request(0)))
	}
	child := llm.request(1)
	if hasTool(child, spawnAgentTool) {
		t.Errorf("subagent was offered %s (no recursion allowed)", spawnAgentTool)
	}
	for _, w := range []string{"write_file", "str_replace"} {
		if hasTool(child, w) {
			t.Errorf("read-only subagent was offered %s", w)
		}
	}
	b, _ := json.Marshal(child["messages"])
	if !strings.Contains(string(b), "find the config parser") {
		t.Errorf("subagent never saw its task: %s", b)
	}
	if strings.Contains(string(b), "investigate") {
		t.Errorf("subagent saw the parent's conversation: %s", b)
	}
	results := toolResults(llm.request(2))
	if len(results) != 1 || !strings.Contains(results[0], "the parser is in config.go") {
		t.Errorf("parent did not get the subagent's answer as the tool result: %v", results)
	}
}

func TestSubagentCannotRunApprovalTierCommands(t *testing.T) {
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{calls: []FunctionCall{toolCall(spawnAgentTool, map[string]any{"task": "fetch something"})}}
		case 2: // the subagent tries a command that normally needs approval
			return fakeReply{calls: []FunctionCall{toolCall("bash", map[string]any{"command": "curl http://example.com"})}}
		case 3:
			return fakeReply{text: "could not run it"}
		}
		return fakeReply{text: "done"}
	})
	s, _ := newTestSession(t, url)
	var c collector
	// noApprover fails the test if the subagent's command ever reaches a human.
	if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	results := toolResults(llm.request(2))
	if len(results) != 1 || !strings.Contains(strings.ToLower(results[0]), "denied") {
		t.Errorf("subagent's approval-tier command was not refused: %v", results)
	}
}

func TestSubagentModelFromEnvAndDefault(t *testing.T) {
	run := func() string {
		llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
			if n == 1 {
				return fakeReply{calls: []FunctionCall{toolCall(spawnAgentTool, map[string]any{"task": "x"})}}
			}
			return fakeReply{text: "ok"}
		})
		s, _ := newTestSession(t, url)
		var c collector
		if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
			t.Fatal(err)
		}
		return llm.request(1)["model"].(string)
	}
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	if got := run(); got != "test-model" {
		t.Errorf("default subagent model = %q, want the parent's %q", got, "test-model")
	}
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "small-model")
	if got := run(); got != "small-model" {
		t.Errorf("subagent model = %q, want %q", got, "small-model")
	}
}

func TestSpawnAgentRequiresTask(t *testing.T) {
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: []FunctionCall{toolCall(spawnAgentTool, map[string]any{})}}
		}
		return fakeReply{text: "ok"}
	})
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("a task-less spawn_agent should not start a subagent; got %d model requests", len(llm.requests))
	}
	if r := toolResults(llm.request(1)); len(r) != 1 || !strings.Contains(r[0], "task") {
		t.Errorf("tool result = %v", r)
	}
}

func TestSpawnAgentAvailableInPlanMode(t *testing.T) {
	llm, url := newFakeLLM(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	s, _ := newTestSession(t, url)
	s.SetSettings(Settings{Plan: true})
	var c collector
	if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	if !hasTool(llm.request(0), spawnAgentTool) {
		t.Errorf("a read-only subagent should be available in Plan mode: %v", toolNames(llm.request(0)))
	}
}

func TestSubagentModelFor(t *testing.T) {
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	if got := subagentModelFor(Settings{Model: "main"}); got != "main" {
		t.Fatalf("no env, no choice: got %q, want the session model", got)
	}
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "from-env")
	if got := subagentModelFor(Settings{Model: "main"}); got != "from-env" {
		t.Fatalf("env set: got %q", got)
	}
	if got := subagentModelFor(Settings{Model: "main", SubagentModel: "chosen"}); got != "chosen" {
		t.Fatalf("/subagent choice must win over the env var: got %q", got)
	}
	if got := subagentModelFor(Settings{Model: "main", SubagentModel: "  "}); got != "from-env" {
		t.Fatalf("blank choice must fall back to the env var: got %q", got)
	}
}

func TestSubagentModelPersists(t *testing.T) {
	t.Setenv("GOHARNESS_SUBAGENT_MODEL", "")
	path := filepath.Join(t.TempDir(), "s.json")
	root := t.TempDir()
	s := NewSession("http://unused", "default-model", root, path, nil)
	if err := s.SetSettings(Settings{Model: "main", SubagentModel: "sub-model", MaxRounds: 5}); err != nil {
		t.Fatal(err)
	}
	st := NewSession("http://unused", "default-model", root, path, nil).Settings()
	if st.SubagentModel != "sub-model" || subagentModelFor(st) != "sub-model" {
		t.Fatalf("reloaded settings = %+v", st)
	}
	// Choosing the default again clears it.
	st.SubagentModel = ""
	if err := s.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := NewSession("http://unused", "default-model", root, path, nil).Settings().SubagentModel; got != "" {
		t.Fatalf("cleared choice still set: %q", got)
	}
}

func TestSpawnAgentDescriptionTellsTheModelWhenToDelegate(t *testing.T) {
	var desc string
	for _, ts := range toolSchemas {
		if ts.Function.Name == spawnAgentTool {
			desc = ts.Function.Description
		}
	}
	for _, want := range []string{"review", "audit", "even in a small project", "ls -R", "file:line"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the spawn_agent description should mention %q:\n%s", want, desc)
		}
	}
}

func TestSubagentSystemPromptHasNoSkillsList(t *testing.T) {
	home, _ := os.UserHomeDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "listed-skill", "---\nname: listed-skill\ndescription: shows up for the parent only\n---\nbody", nil)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".agents")) })
	llm, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		switch n {
		case 1:
			return fakeReply{calls: []FunctionCall{toolCall(spawnAgentTool, map[string]any{"task": "look around"})}}
		case 2:
			return fakeReply{text: "nothing"}
		}
		return fakeReply{text: "done"}
	})
	s, _ := newTestSession(t, url)
	var c collector
	if err := s.Run(context.Background(), "go", nil, c.sink, noApprover(t)); err != nil {
		t.Fatal(err)
	}
	parent, _ := json.Marshal(llm.request(0)["messages"])
	child, _ := json.Marshal(llm.request(1)["messages"])
	if !strings.Contains(string(parent), "listed-skill") {
		t.Errorf("the parent should see the skills list")
	}
	if strings.Contains(string(child), "listed-skill") || strings.Contains(string(child), "SKILLS.") {
		t.Errorf("the subagent's prompt carries the skills list: %.300s", child)
	}
}

func TestSpawnAgentDescriptionTellsTheModelToSplitWorkIntoParallelCalls(t *testing.T) {
	var desc string
	for _, tool := range fullTools {
		if tool.Function.Name == spawnAgentTool {
			desc = tool.Function.Description
		}
	}
	for _, want := range []string{"independent parts", "SAME reply", "at the same time"} {
		if !strings.Contains(desc, want) {
			t.Errorf("spawn_agent description lacks %q:\n%s", want, desc)
		}
	}
}
