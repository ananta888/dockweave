package credentialbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionMintValidateRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s, err := OpenSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	token, sess, err := s.Mint("agent-a", "gr_1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "dws_") {
		t.Fatalf("token prefix: %q", token)
	}
	got, err := s.Validate(token, time.Now())
	if err != nil || got.ID != sess.ID {
		t.Fatalf("validate: %+v %v", got, err)
	}
	// The raw token must not be persisted.
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), token) {
		t.Fatal("raw session token persisted to disk")
	}
	if err := s.Revoke(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(token, time.Now()); err != ErrSessionInvalid {
		t.Fatalf("revoked token accepted: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s, _ := OpenSessions(filepath.Join(t.TempDir(), "sessions.json"))
	token, _, err := s.Mint("a", "g", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(token, time.Now().Add(2*time.Second)); err != ErrSessionInvalid {
		t.Fatalf("expired token accepted: %v", err)
	}
}

func TestSessionRestartPreservesHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s, _ := OpenSessions(path)
	token, sess, err := s.Mint("a", "g", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := OpenSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Validate(token, time.Now())
	if err != nil || got.ID != sess.ID {
		t.Fatalf("session lost across restart: %v", err)
	}
}

func TestSessionTTLBounds(t *testing.T) {
	s, _ := OpenSessions(filepath.Join(t.TempDir(), "sessions.json"))
	if _, _, err := s.Mint("a", "g", 0); err == nil {
		t.Fatal("zero ttl accepted")
	}
	if _, _, err := s.Mint("a", "g", 25*time.Hour); err == nil {
		t.Fatal("ttl over 24h accepted")
	}
}
