package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// logPath points the decision log at a temp file for one test.
func logPath(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "decisions.jsonl")
	t.Setenv("GOHARNESS_DECISION_LOG", p)
	return p
}

func readDecisions(t *testing.T, path string) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// runCalls drives one session turn whose first model reply is the given tool calls.
func runCalls(t *testing.T, st Settings, approve Approver, calls ...FunctionCall) {
	t.Helper()
	root := t.TempDir()
	_, url := newFakeLLM(t, func(n int, _ map[string]any) fakeReply {
		if n == 1 {
			return fakeReply{calls: calls}
		}
		return fakeReply{text: "done"}
	})
	s := NewSession(url, "m", root, "", nil)
	s.SetSettings(st)
	var c collector
	if err := s.Run(context.Background(), "go", nil, c.sink, approve); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionLogRecordsAllowedCall(t *testing.T) {
	p := logPath(t)
	runCalls(t, Settings{}, noApprover(t), toolCall("read_file", map[string]any{"path": "missing.txt"}))
	recs := readDecisions(t, p)
	if len(recs) != 1 {
		t.Fatalf("got %d records: %v", len(recs), recs)
	}
	r := recs[0]
	if r["tool"] != "read_file" || r["tier"] != "allow" || r["decision"] != "allowed" || r["by"] != "policy" {
		t.Fatalf("record = %v", r)
	}
	if r["model"] != "m" || r["time"] == "" || r["input"] != "missing.txt" {
		t.Fatalf("record = %v", r)
	}
	if r["outcome"] != "error" {
		t.Fatalf("a failed read should log outcome error, got %v", r["outcome"])
	}
}

func TestDecisionLogRecordsUserApprovalAndDenial(t *testing.T) {
	p := logPath(t)
	root := t.TempDir()
	n := 0
	approve := func(context.Context, ToolCall, string) (bool, error) { n++; return n == 1, nil }
	runCalls(t, Settings{}, approve,
		toolCall("bash", map[string]any{"command": "touch " + filepath.Join(root, "a")}),
		toolCall("bash", map[string]any{"command": "touch " + filepath.Join(root, "b")}))
	recs := readDecisions(t, p)
	if len(recs) != 2 {
		t.Fatalf("got %d records", len(recs))
	}
	if recs[0]["tier"] != "ask" || recs[0]["decision"] != "allowed" || recs[0]["by"] != "user" || recs[0]["outcome"] != "ok" {
		t.Fatalf("approved = %v", recs[0])
	}
	if recs[1]["decision"] != "denied" || recs[1]["by"] != "user" || recs[1]["outcome"] != "not-run" {
		t.Fatalf("denied = %v", recs[1])
	}
}

func TestDecisionLogRecordsCancelledApproval(t *testing.T) {
	p := logPath(t)
	approve := func(context.Context, ToolCall, string) (bool, error) { return false, context.Canceled }
	runCalls(t, Settings{}, approve, toolCall("bash", map[string]any{"command": "touch /tmp/x-never"}))
	recs := readDecisions(t, p)
	if len(recs) != 1 || recs[0]["outcome"] != "cancelled" {
		t.Fatalf("records = %v", recs)
	}
}

func TestDecisionLogRecordsAutoMode(t *testing.T) {
	p := logPath(t)
	runCalls(t, Settings{Auto: true}, noApprover(t), toolCall("bash", map[string]any{"command": "touch " + filepath.Join(t.TempDir(), "f")}))
	recs := readDecisions(t, p)
	if len(recs) != 1 || recs[0]["tier"] != "ask" || recs[0]["by"] != "auto-mode" || recs[0]["decision"] != "allowed" {
		t.Fatalf("records = %v", recs)
	}
}

func TestDecisionLogRecordsPlanModeDenial(t *testing.T) {
	p := logPath(t)
	runCalls(t, Settings{Plan: true}, noApprover(t), toolCall("write_file", map[string]any{"path": filepath.Join(t.TempDir(), "f"), "content": "SECRET BODY"}))
	recs := readDecisions(t, p)
	if len(recs) != 1 || recs[0]["decision"] != "denied" || recs[0]["by"] != "plan-mode" || recs[0]["outcome"] != "not-run" {
		t.Fatalf("records = %v", recs)
	}
	if recs[0]["input"] == "SECRET BODY" {
		t.Fatal("file contents must not be logged")
	}
}

func TestDecisionLogRedactsSecrets(t *testing.T) {
	p := logPath(t)
	runCalls(t, Settings{Auto: true}, noApprover(t), toolCall("bash", map[string]any{"command": "echo ghp_abcdefghijklmnop1234"}))
	recs := readDecisions(t, p)
	if len(recs) != 1 || recs[0]["input"] != "[redacted]" {
		t.Fatalf("records = %v", recs)
	}
}

func TestDecisionLogCanBeSwitchedOff(t *testing.T) {
	t.Setenv("GOHARNESS_DECISION_LOG", "off")
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".goharness", "decisions.jsonl")
	before := len(readDecisions(t, def)) // other tests share this home and may have logged
	runCalls(t, Settings{}, noApprover(t), toolCall("read_file", map[string]any{"path": "missing.txt"}))
	if after := len(readDecisions(t, def)); after != before {
		t.Fatalf("log grew from %d to %d records although switched off", before, after)
	}
}

func TestDecisionLogDefaultsToHomeFile(t *testing.T) {
	t.Setenv("GOHARNESS_DECISION_LOG", "")
	home, _ := os.UserHomeDir()
	runCalls(t, Settings{}, noApprover(t), toolCall("read_file", map[string]any{"path": "missing.txt"}))
	recs := readDecisions(t, filepath.Join(home, ".goharness", "decisions.jsonl"))
	if len(recs) == 0 {
		t.Fatal("default log file not written")
	}
}
