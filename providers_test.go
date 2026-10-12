package main

import "testing"

func TestResolveProviderFromDefaultsToLlamaSwap(t *testing.T) {
	for _, name := range []string{"", "llama-swap", "bogus-unknown-provider"} {
		baseURL, apiKey, fixed := resolveProviderFrom("http://default/v1", name)
		if baseURL != "http://default/v1" || apiKey != "" || fixed != nil {
			t.Errorf("name=%q: got (%q, %q, %v)", name, baseURL, apiKey, fixed)
		}
	}
}

func TestResolveProviderFromFreellmapi(t *testing.T) {
	t.Setenv("FREELLMAPI_API_KEY", "test-key-123")
	baseURL, apiKey, fixed := resolveProviderFrom("http://default/v1", "freellmapi")
	if baseURL != "http://172.16.10.40:3002/v1" {
		t.Errorf("baseURL = %q", baseURL)
	}
	if apiKey != "test-key-123" {
		t.Errorf("apiKey = %q", apiKey)
	}
	if len(fixed) == 0 {
		t.Fatal("expected a fixed model list")
	}
	found := false
	for _, m := range fixed {
		if m == "auto" {
			found = true
		}
	}
	if !found {
		t.Error("expected \"auto\" in the fixed model list")
	}
}

func TestResolveProviderFromMissingAPIKey(t *testing.T) {
	t.Setenv("FREELLMAPI_API_KEY", "")
	_, apiKey, _ := resolveProviderFrom("http://default/v1", "freellmapi")
	if apiKey != "" {
		t.Errorf("apiKey = %q, want empty when the env var isn't set", apiKey)
	}
}

func TestProviderNamesListsLlamaSwapFirst(t *testing.T) {
	names := providerNames()
	if len(names) < 2 {
		t.Fatalf("names = %v, want at least llama-swap and freellmapi", names)
	}
	if names[0] != "llama-swap" {
		t.Errorf("names[0] = %q, want llama-swap", names[0])
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("duplicate provider name %q", n)
		}
		seen[n] = true
	}
}

func TestSessionResolveProviderUsesOwnBaseURL(t *testing.T) {
	s := &Session{BaseURL: "http://my-llama-swap/v1"}
	baseURL, apiKey, fixed := s.resolveProvider("")
	if baseURL != "http://my-llama-swap/v1" || apiKey != "" || fixed != nil {
		t.Errorf("got (%q, %q, %v)", baseURL, apiKey, fixed)
	}
}
