package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forgetCustomProviders gives a test an empty provider list and its own HOME, so the saved file is its own.
func forgetCustomProviders(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	customMu.Lock()
	customProviders = nil
	customMu.Unlock()
	t.Cleanup(func() {
		customMu.Lock()
		customProviders = nil
		customMu.Unlock()
	})
	return filepath.Join(home, ".config", "goharness", "custom_providers.json")
}

func TestCustomProviderSaveReloadDelete(t *testing.T) {
	path := forgetCustomProviders(t)
	p := provider{Name: "litellm", BaseURL: "http://h:4000/v1", Models: []string{"gpt-x"}, APIKeyEnv: "LK"}
	if err := saveCustomProvider(p); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file should be mode 600: %v %v", fi, err)
	}
	customMu.Lock()
	customProviders = nil // as after a restart
	customMu.Unlock()
	if err := loadCustomProviders(); err != nil {
		t.Fatal(err)
	}
	got := listCustomProviders()
	if len(got) != 1 || got[0].Name != "litellm" || got[0].APIKeyEnv != "LK" || got[0].Models[0] != "gpt-x" {
		t.Fatalf("after reload: %+v", got)
	}
	p.BaseURL = "http://other:4000/v1" // saving the same name replaces it
	if err := saveCustomProvider(p); err != nil || len(listCustomProviders()) != 1 || listCustomProviders()[0].BaseURL != p.BaseURL {
		t.Fatalf("replace: %v %+v", err, listCustomProviders())
	}
	if err := deleteCustomProvider("litellm"); err != nil || len(listCustomProviders()) != 0 {
		t.Fatalf("delete: %v", err)
	}
	if err := deleteCustomProvider("litellm"); err == nil {
		t.Fatal("deleting a missing provider should fail")
	}
}

func TestCustomProviderValidation(t *testing.T) {
	forgetCustomProviders(t)
	ok := provider{Name: "good", BaseURL: "https://x/v1", Models: []string{"m"}}
	for name, mut := range map[string]func(*provider){
		"bad name":          func(p *provider) { p.Name = "Bad Name" },
		"reserved name":     func(p *provider) { p.Name = "llama-swap" },
		"built-in clash":    func(p *provider) { p.Name = "freellmapi" },
		"ftp url":           func(p *provider) { p.BaseURL = "ftp://x/v1" },
		"no host":           func(p *provider) { p.BaseURL = "http://" },
		"no model":          func(p *provider) { p.Models = nil },
		"empty model":       func(p *provider) { p.Models = []string{" "} },
		"key and key env":   func(p *provider) { p.APIKey, p.APIKeyEnv = "k", "E" },
		"name too long":     func(p *provider) { p.Name = strings.Repeat("a", 31) },
		"name starts a dot": func(p *provider) { p.Name = ".x" },
	} {
		p := ok
		mut(&p)
		if err := saveCustomProvider(p); err == nil {
			t.Errorf("%s: should have been refused", name)
		}
	}
	if err := saveCustomProvider(ok); err != nil {
		t.Fatalf("a valid provider was refused: %v", err)
	}
}

func TestCustomProviderWithoutModelsListsLiveFromTheServer(t *testing.T) {
	path := forgetCustomProviders(t)
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`[{"name":"live","base_url":"http://h/v1"}]`), 0o600)
	if err := loadCustomProviders(); err != nil {
		t.Fatalf("a provider with no models list should load (it lists live): %v", err)
	}
	if got := listCustomProviders(); len(got) != 1 || got[0].Models != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestBadCustomProvidersFileIsReported(t *testing.T) {
	path := forgetCustomProviders(t)
	os.MkdirAll(filepath.Dir(path), 0o700)
	for name, body := range map[string]string{
		"not json":     `{oops`,
		"bad name":     `[{"name":"Bad","base_url":"http://h/v1"}]`,
		"duplicate":    `[{"name":"a","base_url":"http://h/v1"},{"name":"a","base_url":"http://h/v1"}]`,
		"key and env":  `[{"name":"a","base_url":"http://h/v1","api_key":"k","api_key_env":"E"}]`,
		"ftp url":      `[{"name":"a","base_url":"ftp://h/v1"}]`,
		"missing base": `[{"name":"a"}]`,
	} {
		os.WriteFile(path, []byte(body), 0o600)
		if err := loadCustomProviders(); err == nil {
			t.Errorf("%s: loading should report an error", name)
		}
	}
}

func TestTUIProviderAddListRemove(t *testing.T) {
	path := forgetCustomProviders(t)
	m := readyModel(t)
	m, _ = m.executeCommand("/provider add litellm http://h:4000/v1 gpt-x --key sk-very-secret-123")
	if !strings.Contains(m.transcript, `provider "litellm" added with model "gpt-x"`) {
		t.Fatalf("add:\n%s", m.transcript)
	}
	if strings.Contains(m.transcript, "sk-very-secret-123") {
		t.Fatalf("the key must never be printed:\n%s", m.transcript)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "sk-very-secret-123") { // stored, but only in the 0600 file
		t.Fatalf("key not stored: %s", data)
	}
	m, _ = m.executeCommand("/provider list")
	if !strings.Contains(m.transcript, "litellm") || !strings.Contains(m.transcript, "gpt-x") || strings.Contains(m.transcript, "sk-very-secret-123") {
		t.Fatalf("list:\n%s", m.transcript)
	}
	m, _ = m.executeCommand("/provider remove litellm")
	if len(listCustomProviders()) != 0 || !strings.Contains(m.transcript, `provider "litellm" removed`) {
		t.Fatalf("remove:\n%s", m.transcript)
	}
}

func TestTUIProviderAddMistakesPrintHelpAndSaveNothing(t *testing.T) {
	forgetCustomProviders(t)
	for _, line := range []string{
		"/provider add onlyname",
		"/provider add litellm ftp://h/v1 m",
		`/provider add "Bad Name" http://h/v1 m`,
		"/provider add a http://h/v1 m --key",
		"/provider add a http://h/v1 m --key k --key-env E",
		"/provider add a http://h/v1 m --nope",
		"/provider add freellmapi http://h/v1 m",
	} {
		m := readyModel(t)
		before := m.transcript
		m, _ = m.executeCommand(line)
		if m.transcript == before {
			t.Errorf("%q printed nothing", line)
		}
		if len(listCustomProviders()) != 0 {
			t.Fatalf("%q saved something: %+v", line, listCustomProviders())
		}
	}
}

func TestWebProvidersAddListRemoveAndKeysStayPrivate(t *testing.T) {
	forgetCustomProviders(t)
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	resp := apiReq(t, srv, "POST", "/api/providers", map[string]any{"name": "litellm", "baseUrl": "http://h:4000/v1", "model": "gpt-x", "apiKey": "sk-web-secret-456"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = apiReq(t, srv, "POST", "/api/providers", map[string]any{"name": "bad name", "baseUrl": "http://h/v1", "model": "m"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "must match") {
		t.Fatalf("bad name: %d %s", resp.StatusCode, body)
	}
	resp = apiReq(t, srv, "GET", "/api/providers", nil)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "sk-web-secret-456") {
		t.Fatalf("the API leaked the key: %s", raw)
	}
	var list []struct {
		ID        string   `json:"id"`
		Custom    bool     `json:"custom"`
		HasAPIKey bool     `json:"hasApiKey"`
		Models    []string `json:"models"`
	}
	json.Unmarshal(raw, &list)
	var found bool
	for _, p := range list {
		if p.ID == "litellm" {
			found = true
			if !p.Custom || !p.HasAPIKey || len(p.Models) != 1 || p.Models[0] != "gpt-x" {
				t.Errorf("litellm entry: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("litellm missing from %s", raw)
	}
	if r := apiReq(t, srv, "DELETE", "/api/providers/litellm", nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if r := apiReq(t, srv, "DELETE", "/api/providers/litellm", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("delete again: %d", r.StatusCode)
	}
}
