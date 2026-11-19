package credentialbroker

import (
	"strings"
	"testing"
)

// TestDefaultProviderAdapters pins each built-in adapter's auth material and
// base URL so a provider can never silently change its credential injection.
func TestDefaultProviderAdapters(t *testing.T) {
	provs := DefaultProviders()
	want := map[string]struct {
		baseURL    string
		authHeader string
		authPrefix string
	}{
		"openrouter":    {"https://openrouter.ai/api/v1", "Authorization", "Bearer "},
		"openai":        {"https://api.openai.com/v1", "Authorization", "Bearer "},
		"deepseek":      {"https://api.deepseek.com/v1", "Authorization", "Bearer "},
		"anthropic":     {"https://api.anthropic.com/v1", "x-api-key", ""},
		"gemini":        {"https://generativelanguage.googleapis.com/v1beta", "x-goog-api-key", ""},
		"github":        {"https://api.githubcopilot.com", "Authorization", "Bearer "},
		"opencode":      {"https://opencode.ai/zen/v1", "Authorization", "Bearer "},
		"codex-chatgpt": {"https://chatgpt.com/backend-api/codex", "Authorization", "Bearer "},
	}
	for name, w := range want {
		p, ok := provs[name]
		if !ok {
			t.Errorf("provider %q missing from DefaultProviders", name)
			continue
		}
		if p.BaseURL != w.baseURL {
			t.Errorf("%s base_url = %q, want %q", name, p.BaseURL, w.baseURL)
		}
		if p.AuthHeader != w.authHeader {
			t.Errorf("%s auth_header = %q, want %q", name, p.AuthHeader, w.authHeader)
		}
		if p.AuthPrefix != w.authPrefix {
			t.Errorf("%s auth_prefix = %q, want %q", name, p.AuthPrefix, w.authPrefix)
		}
		if !strings.HasPrefix(p.BaseURL, "https://") {
			t.Errorf("%s base_url must be https, got %q", name, p.BaseURL)
		}
	}
}

// TestProviderValidation ensures malformed adapters are rejected, so a
// misconfigured providers file fails the broker start instead of leaking.
func TestProviderValidation(t *testing.T) {
	for _, p := range DefaultProviders() {
		if err := validateProvider(p); err != nil {
			t.Errorf("built-in provider %q failed validation: %v", p.Name, err)
		}
	}
	if err := validateProvider(Provider{Name: "x", BaseURL: "https://example.com", AuthHeader: ""}); err == nil {
		t.Error("provider without auth_header must be rejected")
	}
}
