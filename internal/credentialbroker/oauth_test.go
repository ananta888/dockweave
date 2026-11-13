package credentialbroker

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/credentialvault"
)

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return hdr + "." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

func newOAuthFixture(t *testing.T, secretValue string, p Provider) *fixture {
	t.Helper()
	dir := t.TempDir()
	v, err := credentialvault.Create(filepath.Join(dir, "vault.json"), "test passphrase 123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Put("codex-main", p.Name, secretValue); err != nil {
		t.Fatal(err)
	}
	pol, err := OpenPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := pol.Add(Grant{Agent: "agent-a", Credential: "codex-main", Provider: p.Name,
		Models: []string{"*"}, Limits: DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := OpenSessions(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := sess.Mint("agent-a", g.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bud, err := OpenBudget(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{Vault: v, Policy: pol, Sessions: sess, Budget: bud,
		Providers: map[string]Provider{p.Name: p}, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{broker: srv, policy: pol, sessions: sess, budget: bud, token: token, grant: g, vault: v}
}

func TestOAuthBundleRefreshesAndPersists(t *testing.T) {
	var tokenCalls atomic.Int32
	idToken := fakeJWT(t, map[string]any{
		"exp": time.Now().Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "acct-123",
		},
	})
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" ||
			r.Form.Get("client_id") != "client-test" {
			t.Errorf("refresh form = %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-token", "refresh_token": "refresh-2",
			"expires_in": 3600, "id_token": idToken,
		})
	}))
	defer tokenSrv.Close()

	var gotAuth, gotAccount, gotBeta string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotBeta = r.Header.Get("OpenAI-Beta")
		_, _ = w.Write([]byte(`{"id":"resp_1"}`))
	}))
	defer upstream.Close()

	prov := Provider{
		Name: "codex-chatgpt", BaseURL: upstream.URL, AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		Headers: map[string]string{"OpenAI-Beta": "responses=experimental"},
		OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "client-test",
			AccountIDHeader: "chatgpt-account-id"},
	}
	expired := oauthBundle{
		Type: "oauth", AccessToken: "old-token", RefreshToken: "refresh-1",
		ExpiresAt: time.Now().Add(-time.Hour), AccountID: "acct-old",
	}
	raw, _ := expired.marshal()
	f := newOAuthFixture(t, raw, prov)

	resp := f.do(t, http.MethodPost, "/p/codex-chatgpt/responses", `{"model":"gpt-6.1-sol"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotAuth != "Bearer new-token" {
		t.Errorf("upstream auth = %q", gotAuth)
	}
	if gotAccount != "acct-123" {
		t.Errorf("account header = %q", gotAccount)
	}
	if gotBeta != "responses=experimental" {
		t.Errorf("static header = %q", gotBeta)
	}
	sec, err := f.vault.Get("codex-main")
	if err != nil {
		t.Fatal(err)
	}
	var persisted oauthBundle
	if err := json.Unmarshal([]byte(sec.Value), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.AccessToken != "new-token" || persisted.RefreshToken != "refresh-2" || persisted.AccountID != "acct-123" {
		t.Errorf("persisted bundle = %+v", persisted)
	}
	if !persisted.ExpiresAt.After(time.Now()) {
		t.Errorf("persisted expiry not in the future: %v", persisted.ExpiresAt)
	}

	// A second request must reuse the fresh token, not refresh again.
	if resp := f.do(t, http.MethodPost, "/p/codex-chatgpt/responses", `{}`, nil); resp.StatusCode != 200 {
		t.Fatalf("second status = %d", resp.StatusCode)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Errorf("token endpoint calls = %d, want 1", got)
	}
}

func TestOAuthValidTokenSkipsRefresh(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("token endpoint must not be called for a valid token")
	}))
	defer tokenSrv.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer valid-token" {
			t.Errorf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	prov := Provider{Name: "codex-chatgpt", BaseURL: upstream.URL, AuthHeader: "Authorization",
		AuthPrefix: "Bearer ", OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "c"}}
	b := oauthBundle{Type: "oauth", AccessToken: "valid-token", ExpiresAt: time.Now().Add(time.Hour)}
	raw, _ := b.marshal()
	f := newOAuthFixture(t, raw, prov)
	if resp := f.do(t, http.MethodPost, "/p/codex-chatgpt/responses", `{}`, nil); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestOAuthRefreshFailureIsBadGateway(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer tokenSrv.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called when refresh fails")
	}))
	defer upstream.Close()

	prov := Provider{Name: "codex-chatgpt", BaseURL: upstream.URL, AuthHeader: "Authorization",
		AuthPrefix: "Bearer ", OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "c"}}
	b := oauthBundle{Type: "oauth", AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Hour)}
	raw, _ := b.marshal()
	f := newOAuthFixture(t, raw, prov)
	if got := f.do(t, http.MethodPost, "/p/codex-chatgpt/responses", `{}`, nil).StatusCode; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
	if strings.Contains(fmtRecent(f), "old") {
		t.Error("credential material leaked into request log")
	}
}

func TestProviderHeadersInjectedForStaticSecret(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Static"); got != "yes" {
			t.Errorf("static header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer plain-key" {
			t.Errorf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	prov := Provider{Name: "openrouter", BaseURL: upstream.URL, AuthHeader: "Authorization",
		AuthPrefix: "Bearer ", Headers: map[string]string{"X-Static": "yes"}}
	f := newOAuthFixture(t, "plain-key", prov)
	if resp := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func fmtRecent(f *fixture) string {
	var out []string
	for _, e := range f.broker.RecentRequests() {
		out = append(out, e.Credential, e.Path)
	}
	return strings.Join(out, " ")
}
