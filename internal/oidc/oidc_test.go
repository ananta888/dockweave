package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testStore() CookieStore {
	var key [32]byte
	_, _ = rand.Read(key[:])
	return CookieStore{Name: "dockweave_oidc", Key: key}
}

func TestCookieStoreRoundTripAndTamper(t *testing.T) {
	store := testStore()
	now := time.Now()
	session := Session{
		ID: "s1", Principal: Principal{Issuer: "https://idp", Subject: "u1", Roles: []string{"admin"}},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}

	rec := httptest.NewRecorder()
	if err := store.Save(rec, session); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	got, err := store.Get(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Principal.Subject != "u1" || !got.Principal.HasRole(RoleAdmin) {
		t.Fatalf("session = %+v", got)
	}

	// Tampered cookie -> invalid.
	tampered := *cookie
	// Flip one character in the middle (always a real change; the last
	// base64 characters may only carry padding bits).
	mid := len(tampered.Value) / 2
	flip := byte('A')
	if tampered.Value[mid] == 'A' {
		flip = 'B'
	}
	tampered.Value = tampered.Value[:mid] + string(flip) + tampered.Value[mid+1:]
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&tampered)
	if _, err := store.Get(req); err == nil {
		t.Fatal("tampered cookie accepted")
	}

	// Expired session -> no session.
	expired := Session{
		ID: "s2", Principal: Principal{Subject: "u1"},
		IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	rec = httptest.NewRecorder()
	_ = store.Save(rec, expired)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if _, err := store.Get(req); err != ErrNoSession {
		t.Fatalf("expired = %v, want ErrNoSession", err)
	}
}

type fakeProvider struct{}

func (fakeProvider) AuthURL(state, nonce, challenge string) (string, error) {
	return "http://idp/auth?state=" + state + "&code_challenge=" + challenge, nil
}
func (fakeProvider) Exchange(context.Context, string, string, string) (*Principal, error) {
	return &Principal{Issuer: "https://idp", Subject: "user-1", Roles: []string{"admin"}}, nil
}
func (fakeProvider) EndSessionURL(state string) string { return "http://idp/logout" }

func TestServiceLoginFlow(t *testing.T) {
	var cfg Config
	cfg.SessionTTL = time.Hour
	svc := NewService(fakeProvider{}, testStore(), cfg)

	// BeginLogin redirects to the IdP and records state.
	rec := httptest.NewRecorder()
	svc.BeginLogin(rec, httptest.NewRequest(http.MethodGet, "/oauth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("begin = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	parsed, _ := url.Parse(loc)
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("no state in redirect")
	}

	// Callback with the state exchanges and starts a session.
	rec = httptest.NewRecorder()
	cb := httptest.NewRequest(http.MethodGet, "/oauth/callback?state="+state+"&code=abc", nil)
	svc.HandleCallback(rec, cb)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d", rec.Code)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != "dockweave_oidc" {
		t.Fatalf("cookie = %s", cookie.Name)
	}

	// Principal resolves from the session cookie.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	p, err := svc.Principal(req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "user-1" || !p.HasRole(RoleAdmin) {
		t.Fatalf("principal = %+v", p)
	}

	// Replayed state is rejected.
	rec = httptest.NewRecorder()
	svc.HandleCallback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?state="+state+"&code=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replay = %d, want 400", rec.Code)
	}
}

func TestServiceAuditor(t *testing.T) {
	var cfg Config
	cfg.SessionTTL = time.Hour
	svc := NewService(fakeProvider{}, testStore(), cfg)
	var events []string
	var principals []Principal
	svc.SetAuditor(func(e string, p Principal) {
		events = append(events, e)
		principals = append(principals, p)
	})

	rec := httptest.NewRecorder()
	svc.BeginLogin(rec, httptest.NewRequest(http.MethodGet, "/oauth/login", nil))
	state, _ := url.Parse(rec.Header().Get("Location"))
	stateVal := state.Query().Get("state")

	rec = httptest.NewRecorder()
	svc.HandleCallback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?state="+stateVal+"&code=abc", nil))
	if len(events) != 1 || events[0] != "login" {
		t.Fatalf("login audit = %v", events)
	}
	if principals[0].Subject != "user-1" {
		t.Fatalf("principal = %+v", principals[0])
	}

	cookie := rec.Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, "/oauth/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	svc.Logout(rec, req)
	if len(events) != 2 || events[1] != "logout" {
		t.Fatalf("logout audit = %v", events)
	}
}

// TestProviderAgainstMockIssuer runs discovery, exchange and verification
// against an in-process OIDC server with a real signed id_token.
func TestProviderAgainstMockIssuer(t *testing.T) {
	key := testKey(t)
	clientID := "dw-client"

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 iss,
			"authorization_endpoint": iss + "/auth",
			"token_endpoint":         iss + "/token",
			"jwks_uri":               iss + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:   key.Public(),
			KeyID: "test",
			Use:   "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		iss := "http://" + r.Host
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
		if err != nil {
			t.Fatal(err)
		}
		signed, err := signer.Sign([]byte(idTokenPayload(iss, clientID)))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600,"id_token":"` + raw + `"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		Issuer: srv.URL, ClientID: clientID, ClientSecret: "cs",
		RedirectURL: "http://localhost/oauth/callback",
		AdminGroups: []string{"dockweave-admin"},
	}
	prov, err := NewProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	authURL, err := prov.AuthURL("state-1", "nonce-1", "challenge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "/auth?") || !strings.Contains(authURL, "code_challenge=challenge-1") {
		t.Fatalf("auth url = %q", authURL)
	}

	p, err := prov.Exchange(context.Background(), "code-1", "verifier-1", "nonce-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if p.Subject != "user-1" || !p.HasRole(RoleAdmin) {
		t.Fatalf("principal = %+v", p)
	}
	// Groups (deduplicated) and the display handle are kept; no other
	// profile claim (the email) reaches the principal.
	if p.Username != "alice" || len(p.Groups) != 2 || p.Groups[1] != "dw-author" || !strings.HasSuffix(p.ID(), "|user-1") {
		t.Fatalf("principal groups/username = %+v", p)
	}
	if b, _ := json.Marshal(p); strings.Contains(string(b), "example.org") {
		t.Fatalf("profile claim leaked: %s", b)
	}

	// Wrong nonce is rejected.
	if _, err := prov.Exchange(context.Background(), "code-1", "verifier-1", "wrong"); err == nil {
		t.Fatal("nonce mismatch accepted")
	}
}

func idTokenPayload(iss, aud string) []byte {
	now := time.Now().Unix()
	return []byte(`{"iss":"` + iss + `","sub":"user-1","aud":"` + aud + `","iat":` +
		itoa(now) + `,"exp":` + itoa(now+3600) + `,"nonce":"nonce-1","groups":["dockweave-admin","dw-author","dw-author"],"preferred_username":"alice","email":"alice@example.org"}`)
}

func itoa(n int64) string {
	return strings.TrimSpace(jsonNumber(n))
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
