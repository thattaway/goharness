package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	customMu        sync.RWMutex
	customProviders []provider
)

var providerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,29}$`)

func customProvidersPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "goharness", "custom_providers.json"), nil
}

func validateProviderName(name string) error {
	if name == "llama-swap" {
		return fmt.Errorf("provider name %q is reserved", name)
	}
	if !providerNameRe.MatchString(name) {
		return fmt.Errorf("provider name %q must match %s (lowercase alphanumeric, ., _, -; 1-30 chars)", name, providerNameRe.String())
	}
	return nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid base URL %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base URL %q must be http:// or https://", raw)
	}
	return nil
}

func validateProvider(p provider) error {
	if err := validateProviderName(p.Name); err != nil {
		return err
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		return err
	}
	if len(p.Models) == 0 {
		return fmt.Errorf("at least one model is required")
	}
	for _, m := range p.Models {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("model names must be non-empty")
		}
	}
	if p.APIKey != "" && p.APIKeyEnv != "" {
		return fmt.Errorf("specify --key or --key-env, not both")
	}
	for _, other := range otherProviders {
		if other.Name == p.Name {
			return fmt.Errorf("provider %q already exists as a built-in", p.Name)
		}
	}
	return nil
}

func loadCustomProviders() error {
	path, err := customProvidersPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			customMu.Lock()
			customProviders = nil
			customMu.Unlock()
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		customMu.Lock()
		customProviders = nil
		customMu.Unlock()
		return nil
	}
	var list []provider
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, p := range list {
		if err := validateProviderName(p.Name); err != nil {
			return fmt.Errorf("invalid provider %q in %s: %w", p.Name, path, err)
		}
		if err := validateBaseURL(p.BaseURL); err != nil {
			return fmt.Errorf("invalid provider %q in %s: %w", p.Name, path, err)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate provider %q in %s", p.Name, path)
		}
		seen[p.Name] = true
		if p.APIKey != "" && p.APIKeyEnv != "" {
			return fmt.Errorf("provider %q has both api_key and api_key_env", p.Name)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	customMu.Lock()
	customProviders = list
	customMu.Unlock()
	return nil
}

func saveCustomProvidersLocked() error {
	path, err := customProvidersPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	list := append([]provider(nil), customProviders...)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func saveCustomProvider(p provider) error {
	if err := validateProvider(p); err != nil {
		return err
	}
	customMu.Lock()
	defer customMu.Unlock()
	for i, existing := range customProviders {
		if existing.Name == p.Name {
			customProviders[i] = p
			return saveCustomProvidersLocked()
		}
	}
	customProviders = append(customProviders, p)
	return saveCustomProvidersLocked()
}

func deleteCustomProvider(name string) error {
	customMu.Lock()
	defer customMu.Unlock()
	idx := -1
	for i, p := range customProviders {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("no custom provider named %q", name)
	}
	customProviders = append(customProviders[:idx], customProviders[idx+1:]...)
	return saveCustomProvidersLocked()
}

func listCustomProviders() []provider {
	customMu.RLock()
	defer customMu.RUnlock()
	out := append([]provider(nil), customProviders...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// probeModels asks an OpenAI-compatible server which models it serves, and says plainly why it could not
// (unreachable, key refused, not an OpenAI-style server, no models).
func probeModels(baseURL, apiKey string) ([]string, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	req, err := http.NewRequest("GET", base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %v", base, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("the server rejected the API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the server answered HTTP %d for %s/models", resp.StatusCode, base)
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("%s/models did not return an OpenAI-style model list", base)
	}
	ids := make([]string, 0, len(data.Data))
	for _, d := range data.Data {
		ids = append(ids, d.ID)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("the server listed no models")
	}
	sort.Strings(ids)
	return ids, nil
}
