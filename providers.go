package main

import (
	"os"
	"sort"
)

// provider describes one OpenAI-compatible backend goharness can talk to.
// llama-swap (the default) always fetches its model list live from its own
// /v1/models, which is accurate. A provider can instead ship a fixed
// Models list - used for freellmapi, whose own "available" flag has been
// observed to list models that don't actually return a completion (see
// the freellmapi cleanup this session): rather than trust a live fetch,
// goharness offers only models that were individually confirmed working.
type provider struct {
	Name      string   `json:"name"`
	BaseURL   string   `json:"base_url"`
	APIKey    string   `json:"api_key,omitempty"`
	APIKeyEnv string   `json:"api_key_env,omitempty"` // env var holding the bearer token; "" if none needed
	Models    []string `json:"models,omitempty"`      // fixed model list; nil means fetch live from BaseURL+"/models"
}

func providerAPIKey(p provider) string {
	if p.APIKeyEnv != "" {
		return os.Getenv(p.APIKeyEnv)
	}
	return p.APIKey
}

// freellmapiModels is a maintained allowlist, not a live catalog fetch -
// see providers.go's doc comment. Re-verify with a real chat completion
// before adding anything here, the same way this list was built.
var freellmapiModels = []string{
	"auto", "fusion",
	"allam-2-7b", "llama-prompt-guard-2-22m", "llama-prompt-guard-2-86m",
	"gpt-oss-120b", "gpt-oss-20b", "gpt-oss-safeguard-20b",
	"north-mini-code", "deepseek-v3", "dots3-note-preview", "lfm2.5-2.6b",
	"nemotron-3-nano-30b-reasoning", "nemotron-3-super-120b",
	"nemotron-3.5-content-safety", "nemotron-3.5-lightning",
	"poolside-laguna-s-2.1", "poolside-laguna-xs-2.1",
	"qwen3-coder-480b", "glm-4.5", "gemma-4-31b", "gemma-4-26b-a4b",
}

// otherProviders lists every provider besides llama-swap, which is special:
// its BaseURL comes from the session's own configured URL (the -base-url
// flag), not a fixed constant, so it isn't in this table - see
// Session.resolveProvider.
var otherProviders = []provider{
	{
		Name:      "freellmapi",
		BaseURL:   "http://172.16.10.40:3002/v1",
		APIKeyEnv: "FREELLMAPI_API_KEY",
		Models:    freellmapiModels,
	},
}

// allProviders returns every non-llama-swap provider, sorted by name.
func allProviders() []provider {
	customMu.RLock()
	custom := append([]provider(nil), customProviders...)
	customMu.RUnlock()
	out := append(append([]provider(nil), otherProviders...), custom...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// providerNames lists every switchable provider, llama-swap first.
func providerNames() []string {
	names := []string{"llama-swap"}
	for _, p := range allProviders() {
		names = append(names, p.Name)
	}
	return names
}

func lookupProvider(name string) (provider, bool) {
	for _, p := range otherProviders {
		if p.Name == name {
			return p, true
		}
	}
	customMu.RLock()
	defer customMu.RUnlock()
	for _, p := range customProviders {
		if p.Name == name {
			return p, true
		}
	}
	return provider{}, false
}

// resolveProviderFrom returns the base URL and API key to use for name
// right now, and the fixed model list to offer instead of a live fetch
// (nil for llama-swap, which fetches live). defaultBaseURL is used for
// "llama-swap" and for any unrecognized or empty name - saved sessions
// from before providers existed have no Settings.Provider, and should
// keep behaving exactly as before.
func resolveProviderFrom(defaultBaseURL, name string) (baseURL, apiKey string, fixedModels []string) {
	if p, ok := lookupProvider(name); ok {
		return p.BaseURL, providerAPIKey(p), p.Models
	}
	return defaultBaseURL, "", nil
}

// resolveProvider is resolveProviderFrom using this session's own
// configured -base-url as the llama-swap default.
func (s *Session) resolveProvider(name string) (baseURL, apiKey string, fixedModels []string) {
	return resolveProviderFrom(s.BaseURL, name)
}
