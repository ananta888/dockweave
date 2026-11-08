package credentialbroker

import (
	"fmt"
	"sort"
)

// Provider describes one upstream API and how its credential is injected.
// BaseURL is fixed per provider; agents cannot choose arbitrary targets.
type Provider struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	AuthHeader string `json:"auth_header"`
	AuthPrefix string `json:"auth_prefix"`
}

// DefaultProviders are the built-in adapters. All are fixed-destination
// OpenAI-compatible or provider-native HTTP APIs; adding a provider is a
// code/config change, never an agent-controlled value.
func DefaultProviders() map[string]Provider {
	list := []Provider{
		{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1", AuthHeader: "Authorization", AuthPrefix: "Bearer "},
		{Name: "openai", BaseURL: "https://api.openai.com/v1", AuthHeader: "Authorization", AuthPrefix: "Bearer "},
		{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", AuthHeader: "Authorization", AuthPrefix: "Bearer "},
		{Name: "anthropic", BaseURL: "https://api.anthropic.com/v1", AuthHeader: "x-api-key"},
		{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta", AuthHeader: "x-goog-api-key"},
	}
	out := make(map[string]Provider, len(list))
	for _, p := range list {
		out[p.Name] = p
	}
	return out
}

// SortedProviders returns provider names in stable order.
func SortedProviders(m map[string]Provider) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func validateProvider(p Provider) error {
	if p.Name == "" || p.BaseURL == "" || p.AuthHeader == "" {
		return fmt.Errorf("provider %q: name, base_url and auth_header are required", p.Name)
	}
	return nil
}
