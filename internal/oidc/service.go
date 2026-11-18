package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// Service orchestrates the authorization-code flow with PKCE.
type Service struct {
	prov  Provider
	store SessionStore
	cfg   Config
	now   func() time.Time

	mu      sync.Mutex
	states  map[string]stateEntry
	auditor func(event string, p Principal)
}

type stateEntry struct {
	verifier string
	nonce    string
	expires  time.Time
}

const stateTTL = 10 * time.Minute

// NewService builds the application service.
func NewService(prov Provider, store SessionStore, cfg Config) *Service {
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	cfg.SessionTTL = ttl
	return &Service{
		prov: prov, store: store, cfg: cfg,
		now: time.Now, states: map[string]stateEntry{},
	}
}

// SetAuditor installs a callback invoked on successful login and on logout.
// The callback receives only the event name and the minimal principal.
func (s *Service) SetAuditor(fn func(event string, p Principal)) { s.auditor = fn }

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// BeginLogin redirects the browser to the IdP.
func (s *Service) BeginLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomHex(16)
	if err != nil {
		http.Error(w, "state", http.StatusInternalServerError)
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		http.Error(w, "nonce", http.StatusInternalServerError)
		return
	}
	verifier, err := randomVerifier()
	if err != nil {
		http.Error(w, "verifier", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.states[state] = stateEntry{verifier: verifier, nonce: nonce, expires: s.now().Add(stateTTL)}
	s.mu.Unlock()

	url, err := s.prov.AuthURL(state, nonce, s.challenge(verifier))
	if err != nil {
		http.Error(w, "auth url", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// HandleCallback exchanges the code, verifies the identity and starts a
// session. On success the browser is redirected back to the app root.
func (s *Service) HandleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	s.mu.Lock()
	entry, ok := s.states[state]
	delete(s.states, state)
	s.mu.Unlock()

	if !ok || entry.expires.Before(s.now()) || code == "" {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// Nonce binding is enforced by the provider's id_token verification.
	principal, err := s.prov.Exchange(context.Background(), code, entry.verifier, entry.nonce)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	now := s.now()
	session := Session{
		ID:        randomSessionID(),
		Principal: *principal,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
	}
	if err := s.store.Save(w, session); err != nil {
		http.Error(w, "session", http.StatusInternalServerError)
		return
	}
	if s.auditor != nil {
		s.auditor("login", session.Principal)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout clears the session and redirects to the IdP end-session endpoint.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if s.auditor != nil {
		if p, err := s.Principal(r); err == nil {
			s.auditor("logout", *p)
		}
	}
	s.store.Delete(w)
	state, _ := randomHex(8)
	http.Redirect(w, r, s.prov.EndSessionURL(state), http.StatusSeeOther)
}

// Principal returns the authenticated principal from the session cookie.
func (s *Service) Principal(r *http.Request) (*Principal, error) {
	session, err := s.store.Get(r)
	if err != nil {
		return nil, err
	}
	return &session.Principal, nil
}

func randomSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
