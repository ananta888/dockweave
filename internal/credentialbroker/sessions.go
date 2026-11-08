package credentialbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// ErrSessionInvalid is returned for unknown, expired or revoked sessions.
var ErrSessionInvalid = errors.New("session invalid, expired or revoked")

// Session is a short-lived broker capability. The raw token is returned once
// by Mint and only its SHA-256 hash is persisted.
type Session struct {
	ID        string     `json:"id"`
	TokenHash string     `json:"token_hash"`
	Agent     string     `json:"agent"`
	GrantID   string     `json:"grant_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type sessionFile struct {
	Version  int       `json:"version"`
	Sessions []Session `json:"sessions"`
}

// SessionStore persists session hashes so a broker restart invalidates
// nothing except what policy says, while revocation survives restarts.
type SessionStore struct {
	mu       sync.Mutex
	path     string
	byHash   map[string]*Session
	sessions map[string]*Session
}

// OpenSessions loads sessions from path. A missing file starts empty.
func OpenSessions(path string) (*SessionStore, error) {
	s := &SessionStore{path: path, byHash: map[string]*Session{}, sessions: map[string]*Session{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	var f sessionFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse sessions: %w", err)
	}
	for i := range f.Sessions {
		sess := f.Sessions[i]
		s.sessions[sess.ID] = &sess
		s.byHash[sess.TokenHash] = &sess
	}
	return s, nil
}

// Mint creates a session and returns the raw token exactly once.
func (s *SessionStore) Mint(agent, grantID string, ttl time.Duration) (string, Session, error) {
	if agent == "" || grantID == "" {
		return "", Session{}, errors.New("agent and grant are required")
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		return "", Session{}, errors.New("ttl must be between 1s and 24h")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token := "dws_" + randomHex(32)
	now := time.Now().UTC()
	sess := Session{
		ID:        "ses_" + randomHex(12),
		TokenHash: hashToken(token),
		Agent:     agent,
		GrantID:   grantID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	s.sessions[sess.ID] = &sess
	s.byHash[sess.TokenHash] = &sess
	if err := s.saveLocked(); err != nil {
		delete(s.sessions, sess.ID)
		delete(s.byHash, sess.TokenHash)
		return "", Session{}, err
	}
	return token, sess, nil
}

// Validate resolves a raw token to an active session.
func (s *SessionStore) Validate(token string, now time.Time) (*Session, error) {
	if token == "" {
		return nil, ErrSessionInvalid
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byHash[h]
	if !ok || !tokensEqual(sess.TokenHash, h) {
		return nil, ErrSessionInvalid
	}
	if sess.RevokedAt != nil || now.After(sess.ExpiresAt) {
		return nil, ErrSessionInvalid
	}
	cp := *sess
	return &cp, nil
}

// Revoke marks a session revoked. Revocation is effective immediately
// because every request validates against the store.
func (s *SessionStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return errors.New("session not found")
	}
	if sess.RevokedAt == nil {
		now := time.Now().UTC()
		sess.RevokedAt = &now
		return s.saveLocked()
	}
	return nil
}

// List returns sessions, newest first, optionally filtered by agent.
func (s *SessionStore) List(agent string) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if agent == "" || sess.Agent == agent {
			out = append(out, *sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// SessionInfo is the secret-free operator view of a session.
type SessionInfo struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`
	GrantID   string     `json:"grant_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// InfoList returns session metadata without token hashes.
func (s *SessionStore) InfoList(agent string) []SessionInfo {
	list := s.List(agent)
	out := make([]SessionInfo, len(list))
	for i, sess := range list {
		out[i] = SessionInfo{ID: sess.ID, Agent: sess.Agent, GrantID: sess.GrantID,
			CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt, RevokedAt: sess.RevokedAt}
	}
	return out
}

// ActiveCount returns the number of unrevoked, unexpired sessions for agent.
func (s *SessionStore) ActiveCount(agent string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sess := range s.sessions {
		if sess.Agent == agent && sess.RevokedAt == nil && now.Before(sess.ExpiresAt) {
			n++
		}
	}
	return n
}

func (s *SessionStore) saveLocked() error {
	list := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		list = append(list, *sess)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	b, err := json.MarshalIndent(sessionFile{Version: 1, Sessions: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b, 0o600)
}
