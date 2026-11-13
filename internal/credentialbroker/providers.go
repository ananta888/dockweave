package credentialbroker

import (
	"fmt"
	"sort"
	"strings"
)

// Provider describes one upstream API and how its credential is injected.
// BaseURL is fixed per provider; agents cannot choose arbitrary targets.
type Provider struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	AuthHeader string `json:"auth_header"`
	AuthPrefix string `json:"auth_prefix"`
	// Headers are static, operator-defined headers added to every upstream
	// request (e.g. protocol or client markers). They never come from the
	// agent and cannot be overridden by it.
	Headers map[string]string `json:"headers,omitempty"`
	// OAuth marks the credential as an OAuth bundle that the broker refreshes
	// before injecting the access token. The secret value is a JSON bundle, not
	// a bare key.
	OAuth *OAuthConfig `json:"oauth,omitempty"`
}

// OAuthConfig describes how to refresh an OAuth credential bundle.
type OAuthConfig struct {
	// TokenURL is the fixed token endpoint (https only).
	TokenURL string `json:"token_url"`
	// ClientID is the OAuth client id used for the refresh grant.
	ClientID string `json:"client_id"`
	// Scope is optional and sent on refresh when non-empty.
	Scope string `json:"scope,omitempty"`
	// AccountIDHeader receives the bundle's account id (or the id derived from
	// the id_token). Empty means no account header.
	AccountIDHeader string `json:"account_id_header,omitempty"`
	// RefreshSkewSeconds refreshes this many seconds before expiry.
	// Defaults to 120.
	RefreshSkewSeconds int `json:"refresh_skew_seconds,omitempty"`
}

func (o *OAuthConfig) skew() int {
	if o == nil || o.RefreshSkewSeconds <= 0 {
		return 120
	}
	return o.RefreshSkewSeconds
}

// ChatGPT OAuth (Codex CLI). The access token is refreshed at auth.openai.com
// and injected into the fixed ChatGPT Codex Responses backend.
const codexChatGPTClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

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
		{Name: "opencode", BaseURL: "https://opencode.ai/zen/v1", AuthHeader: "Authorization", AuthPrefix: "Bearer "},
		{
			Name: "codex-chatgpt", BaseURL: "https://chatgpt.com/backend-api/codex",
			AuthHeader: "Authorization", AuthPrefix: "Bearer ",
			Headers: map[string]string{
				"OpenAI-Beta": "responses=experimental",
				"originator":  "codex_cli_rs",
			},
			OAuth: &OAuthConfig{
				TokenURL:        "https://auth.openai.com/oauth/token",
				ClientID:        codexChatGPTClientID,
				Scope:           "openid profile email offline_access",
				AccountIDHeader: "chatgpt-account-id",
			},
		},
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
	for k := range p.Headers {
		if err := validateHeaderName(k); err != nil {
			return fmt.Errorf("provider %q: %w", p.Name, err)
		}
	}
	if p.OAuth != nil {
		if strings.TrimSpace(p.OAuth.TokenURL) == "" {
			return fmt.Errorf("provider %q: oauth token_url is required", p.Name)
		}
		if strings.TrimSpace(p.OAuth.ClientID) == "" {
			return fmt.Errorf("provider %q: oauth client_id is required", p.Name)
		}
	}
	return nil
}

func validateHeaderName(k string) error {
	if k == "" {
		return fmt.Errorf("empty header name")
	}
	for _, r := range k {
		if r <= ' ' || r >= 0x7f || r == ':' {
			return fmt.Errorf("invalid header name %q", k)
		}
	}
	return nil
}
