package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// answer types text into the input line and presses Enter, as a person would.
func answer(m model, text string) model {
	m.input.SetValue(text)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(model)
}

func TestProviderWizardWithAKeyVariable(t *testing.T) {
	forgetCustomProviders(t)
	t.Setenv("WIZ_KEY", "set")
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add")
	if m.wizard == nil || !strings.Contains(m.transcript, "Provider name") {
		t.Fatalf("the wizard should start and ask for a name:\n%s", m.transcript)
	}
	m = answer(m, "litellm")
	m = answer(m, "http://h:4000/v1/")
	m = answer(m, "gpt-x")
	if m.wizard == nil || m.wizard.step != wzKeyEnv {
		t.Fatalf("should now ask for the key variable: %+v", m.wizard)
	}
	m = answer(m, "WIZ_KEY")
	if m.wizard != nil {
		t.Fatalf("the wizard should be finished")
	}
	got := listCustomProviders()
	if len(got) != 1 || got[0].Name != "litellm" || got[0].BaseURL != "http://h:4000/v1" || got[0].Models[0] != "gpt-x" || got[0].APIKeyEnv != "WIZ_KEY" {
		t.Fatalf("saved: %+v", got)
	}
	if !strings.Contains(m.transcript, `provider "litellm" added with model "gpt-x"`) {
		t.Fatalf("no confirmation:\n%s", m.transcript)
	}
	if m.input.Placeholder != "message…" {
		t.Fatalf("the input line should be back to normal, placeholder %q", m.input.Placeholder)
	}
}

func TestProviderWizardRawKeyIsHiddenAndNeverPrinted(t *testing.T) {
	forgetCustomProviders(t)
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add")
	m = answer(m, "p1")
	m = answer(m, "https://api.example.com/v1")
	m = answer(m, "m1")
	m = answer(m, "") // no key variable: it asks for the key itself
	if m.wizard == nil || m.wizard.step != wzKey || m.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("should be asking for the key, hidden: %+v echo=%v", m.wizard, m.input.EchoMode)
	}
	m = answer(m, "sk-wizard-secret-789")
	if strings.Contains(m.transcript, "sk-wizard-secret-789") {
		t.Fatalf("the key must never be printed:\n%s", m.transcript)
	}
	got := listCustomProviders()
	if len(got) != 1 || got[0].APIKey != "sk-wizard-secret-789" || got[0].APIKeyEnv != "" {
		t.Fatalf("saved: %+v", got)
	}
	if m.input.EchoMode != textinput.EchoNormal {
		t.Fatalf("typing should be visible again")
	}
}

func TestProviderWizardRepeatsAStepOnABadAnswer(t *testing.T) {
	forgetCustomProviders(t)
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add")
	for _, bad := range []string{"Bad Name", "freellmapi", "llama-swap", ""} {
		m = answer(m, bad)
		if m.wizard == nil || m.wizard.step != wzName {
			t.Fatalf("%q should keep it on the name step: %+v", bad, m.wizard)
		}
	}
	m = answer(m, "ok1")
	m = answer(m, "ftp://h/v1")
	if m.wizard.step != wzURL {
		t.Fatalf("a bad URL should repeat the URL step, at %d", m.wizard.step)
	}
	m = answer(m, "http://h/v1")
	m = answer(m, "")
	if m.wizard.step != wzModel {
		t.Fatalf("an empty model should repeat the model step, at %d", m.wizard.step)
	}
	m = answer(m, "m")
	m = answer(m, "not a var")
	if m.wizard.step != wzKeyEnv || !strings.Contains(m.transcript, "not a valid environment variable name") {
		t.Fatalf("a bad variable name should repeat the step: %+v", m.wizard)
	}
	if len(listCustomProviders()) != 0 {
		t.Fatalf("nothing should be saved yet")
	}
}

func TestProviderWizardEscCancelsAndSavesNothing(t *testing.T) {
	forgetCustomProviders(t)
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add")
	m = answer(m, "half")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.wizard != nil || len(listCustomProviders()) != 0 || !strings.Contains(m.transcript, "cancelled") {
		t.Fatalf("wizard=%v saved=%v\n%s", m.wizard, listCustomProviders(), m.transcript)
	}
	if m.input.Placeholder != "message…" {
		t.Fatalf("placeholder not restored: %q", m.input.Placeholder)
	}
}

func TestProviderAddWithArgumentsStillWorksAndNeedsNoWizard(t *testing.T) {
	forgetCustomProviders(t)
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add p2 http://h/v1 m2 --key-env K2")
	if m.wizard != nil || len(listCustomProviders()) != 1 {
		t.Fatalf("one-line form: wizard=%v saved=%v", m.wizard, listCustomProviders())
	}
}

func modelServer(t *testing.T, wantKey string, models string, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if wantKey != "" && r.Header.Get("Authorization") != "Bearer "+wantKey {
			http.Error(w, `{"error":"no"}`, http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(models))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func TestProbeModels(t *testing.T) {
	good := `{"data":[{"id":"zeta"},{"id":"alpha"}]}`
	if ids, err := probeModels(modelServer(t, "k1", good, 200), "k1"); err != nil || len(ids) != 2 || ids[0] != "alpha" {
		t.Fatalf("good: %v %v", ids, err)
	}
	for name, c := range map[string]struct {
		url, key, want string
	}{
		"wrong key":    {modelServer(t, "k1", good, 200), "nope", "rejected the API key"},
		"server error": {modelServer(t, "", "x", 500), "", "HTTP 500"},
		"not a list":   {modelServer(t, "", "<html>", 200), "", "did not return an OpenAI-style model list"},
		"no models":    {modelServer(t, "", `{"data":[]}`, 200), "", "listed no models"},
		"unreachable":  {"http://127.0.0.1:1/v1", "", "could not reach"},
		"bad scheme":   {"ftp://h/v1", "", "must be http"},
	} {
		if _, err := probeModels(c.url, c.key); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
	}
}

func TestWebProviderModelsEndpoint(t *testing.T) {
	forgetCustomProviders(t)
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	base := modelServer(t, "env-key-1", `{"data":[{"id":"m-b"},{"id":"m-a"}]}`, 200)
	t.Setenv("PROBE_KEY", "env-key-1")
	resp := apiReq(t, srv, "POST", "/api/providers/models", map[string]any{"baseUrl": base, "apiKeyEnv": "PROBE_KEY"})
	got := decode[struct {
		Models []string `json:"models"`
	}](t, resp)
	if len(got.Models) != 2 || got.Models[0] != "m-a" {
		t.Fatalf("models: %+v", got)
	}
	for name, body := range map[string]map[string]any{
		"unset variable": {"baseUrl": base, "apiKeyEnv": "NOT_SET_ANYWHERE_X"},
		"bad variable":   {"baseUrl": base, "apiKeyEnv": "not a var"},
		"both":           {"baseUrl": base, "apiKey": "a", "apiKeyEnv": "PROBE_KEY"},
	} {
		if r := apiReq(t, srv, "POST", "/api/providers/models", body); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r := apiReq(t, srv, "POST", "/api/providers/models", map[string]any{"baseUrl": base, "apiKey": "wrong"}); r.StatusCode != http.StatusBadGateway {
		t.Errorf("a refused key should be a 502, got %d", r.StatusCode)
	}
	_ = os.Getenv
}
