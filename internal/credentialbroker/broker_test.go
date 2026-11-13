package credentialbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/credentialvault"
)

type fixture struct {
	broker   *Server
	policy   *Policy
	sessions *SessionStore
	budget   *Budget
	token    string
	grant    Grant
	vault    *credentialvault.Store
}

func newFixture(t *testing.T, fake *httptest.Server, limits Limits, models []string) *fixture {
	t.Helper()
	dir := t.TempDir()
	v, err := credentialvault.Create(filepath.Join(dir, "vault.json"), "test passphrase 123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Put("openrouter-main", "openrouter", "real-secret-value"); err != nil {
		t.Fatal(err)
	}
	pol, err := OpenPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := pol.Add(Grant{Agent: "agent-a", Credential: "openrouter-main",
		Provider: "openrouter", Models: models, Limits: limits})
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
	providers := map[string]Provider{"openrouter": {
		Name: "openrouter", BaseURL: fake.URL, AuthHeader: "Authorization", AuthPrefix: "Bearer ",
	}}
	srv, err := NewServer(Config{Vault: v, Policy: pol, Sessions: sess, Budget: bud,
		Providers: providers, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{broker: srv, policy: pol, sessions: sess, budget: bud, token: token, grant: g, vault: v}
}

func (f *fixture) do(t *testing.T, method, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.broker.ServeHTTP(rec, req)
	return rec.Result()
}

func TestBrokerInjectsCredentialAndStripsClientAuth(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	resp := f.do(t, http.MethodPost, "/p/openrouter/chat/completions?beta=1",
		`{"model":"openrouter/auto"}`, map[string]string{
			"Authorization":       "Bearer dummy",
			"X-Dockweave-Session": f.token,
			"X-Api-Key":           "dummy2",
		})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotAuth != "Bearer real-secret-value" {
		t.Fatalf("upstream auth = %q, want injected secret", gotAuth)
	}
	if gotPath != "/chat/completions" || gotQuery != "beta=1" {
		t.Fatalf("path/query = %q?%q", gotPath, gotQuery)
	}
	if strings.Contains(fmt.Sprint(f.broker.RecentRequests()), "real-secret-value") {
		t.Fatal("secret leaked into request log")
	}
}

func TestBrokerDeniesWithoutGrant(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	// Another agent cannot use agent-a's grant.
	token, _, err := f.sessions.Mint("agent-b", f.grant.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.token = token
	resp := f.do(t, http.MethodPost, "/p/openrouter/chat/completions", `{"model":"x"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBrokerDeniesDisallowedModel(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"allowed/model"})
	resp := f.do(t, http.MethodPost, "/p/openrouter/chat/completions", `{"model":"other/model"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBrokerRejectsInvalidAndRevokedSession(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	f.token = "dws_not-a-real-token"
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != 401 {
		t.Fatalf("invalid token status = %d, want 401", got)
	}
	f.token = ""
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != 401 {
		t.Fatalf("missing token status = %d, want 401", got)
	}
}

func TestBrokerRevocationIsImmediate(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	sessions := f.sessions.List("agent-a")
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	if err := f.sessions.Revoke(sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != 401 {
		t.Fatalf("revoked session status = %d, want 401", got)
	}
}

func TestBrokerBudgetLimit(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer fake.Close()
	f := newFixture(t, fake, Limits{MaxRequestsPerMinute: 100, MaxConcurrent: 4, MaxRequestsPerDay: 1}, []string{"*"})
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != 200 {
		t.Fatalf("first status = %d", got)
	}
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", got)
	}
}

func TestBrokerConcurrencyLimit(t *testing.T) {
	block := make(chan struct{})
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		_, _ = io.WriteString(w, `{}`)
	}))
	defer fake.Close()
	f := newFixture(t, fake, Limits{MaxRequestsPerMinute: 100, MaxConcurrent: 1}, []string{"*"})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil)
	}()
	time.Sleep(50 * time.Millisecond)
	if got := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil).StatusCode; got != http.StatusTooManyRequests {
		t.Fatalf("concurrent status = %d, want 429", got)
	}
	close(block)
	wg.Wait()
}

func TestBrokerDoesNotFollowRedirects(t *testing.T) {
	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		if r.Header.Get("Authorization") != "" {
			t.Error("credential forwarded to redirect target")
		}
	}))
	defer target.Close()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/evil", http.StatusTemporaryRedirect)
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	resp := f.do(t, http.MethodPost, "/p/openrouter/chat", `{}`, nil)
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", resp.StatusCode)
	}
	if targetHit {
		t.Fatal("broker followed redirect and leaked credential")
	}
}

func TestBrokerStreamingUsage(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fl.Flush()
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":4}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	resp := f.do(t, http.MethodPost, "/p/openrouter/chat/completions", `{"model":"m","stream":true}`, nil)
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(b, []byte("[DONE]")) {
		t.Fatalf("stream not forwarded: %q", b)
	}
	usage := f.budget.Snapshot(f.grant.ID)
	if usage.TokensDay < 15 {
		t.Fatalf("tokens = %d, want >= 15", usage.TokensDay)
	}
}

func TestBrokerRejectsPathTraversalAndUnknownProvider(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	for _, p := range []string{"/p/unknown/chat", "/p/openrouter/../../etc/passwd", "/p/openrouter"} {
		if got := f.do(t, http.MethodPost, p, `{}`, nil).StatusCode; got != 404 {
			t.Errorf("%s status = %d, want 404", p, got)
		}
	}
}

func TestBrokerMetadataCallDoesNotConsumeTokens(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(`{"id":"model-k"},`, 500))
	}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	if got := f.do(t, http.MethodGet, "/p/openrouter/models", "", nil).StatusCode; got != 200 {
		t.Fatalf("status = %d", got)
	}
	if usage := f.budget.Snapshot(f.grant.ID); usage.TokensDay != 0 {
		t.Fatalf("metadata call consumed %d tokens", usage.TokensDay)
	}
	if usage := f.budget.Snapshot(f.grant.ID); usage.RequestsDay != 1 {
		t.Fatalf("metadata call not counted as a request: %d", usage.RequestsDay)
	}
}

func TestAdminSecretMetadataNeverReturnsValue(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	handler, err := NewAdminHandler(AdminConfig{Vault: f.vault, Policy: f.policy,
		Sessions: f.sessions, Budget: f.budget, AllowedUIDs: []uint32{uint32(os.Geteuid())}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withPeerUID(t.Context(), uint32(os.Geteuid()))
	req := httptest.NewRequest(http.MethodGet, "/v1/secrets", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["provider"] != "openrouter" {
		t.Fatalf("unexpected metadata: %v", out)
	}
	if strings.Contains(rec.Body.String(), "real-secret-value") {
		t.Fatal("secret value exposed in metadata response")
	}
}

func withPeerUID(ctx context.Context, uid uint32) context.Context {
	return context.WithValue(ctx, peerUIDKey{}, uid)
}

func TestAdminRejectsForeignPeerUID(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()
	f := newFixture(t, fake, DefaultLimits(), []string{"*"})
	handler, err := NewAdminHandler(AdminConfig{Vault: f.vault, Policy: f.policy,
		Sessions: f.sessions, Budget: f.budget, AllowedUIDs: []uint32{4242}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withPeerUID(t.Context(), 9999)
	req := httptest.NewRequest(http.MethodGet, "/v1/secrets", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign peer status = %d, want 403", rec.Code)
	}
}
