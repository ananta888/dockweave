package credentialbroker

import (
	"strings"
	"testing"
)

// FuzzSplitGatewayPath asserts the gateway path splitter never panics, always
// returns an absolute cleaned upstream, and never accepts path traversal or
// backslashes (the fixed-destination property of the broker proxy).
func FuzzSplitGatewayPath(f *testing.F) {
	f.Add("/p/openrouter/v1/chat/completions")
	f.Add("/p/openai/models")
	f.Add("/healthz")
	f.Add("/p//")
	f.Add("/p/x/../../etc/passwd")
	f.Add("/p/x/a\\..\\b")
	f.Fuzz(func(t *testing.T, s string) {
		_, upstream, ok := splitGatewayPath(s)
		if !ok {
			return
		}
		if upstream == "" || upstream[0] != '/' {
			t.Fatalf("non-absolute upstream %q", upstream)
		}
		if strings.Contains(upstream, "..") || strings.Contains(upstream, "\\") {
			t.Fatalf("traversal/backslash survived: %q", upstream)
		}
	})
}

// FuzzBodyParsing asserts the body parsers used before policy/budget checks
// tolerate arbitrary input without panicking.
func FuzzBodyParsing(f *testing.F) {
	f.Add([]byte(`{"model":"gpt-4o"}`))
	f.Add([]byte(`{"max_tokens":1024}`))
	f.Add([]byte(`{`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, b []byte) {
		_ = extractModel(b)
		p, c := estimateTokens(b)
		if p < 0 || c < 0 {
			t.Fatalf("negative estimate: %d/%d", p, c)
		}
	})
}
