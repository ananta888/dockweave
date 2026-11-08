package credentialbroker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicyDenyByDefaultAndModelAllowlist(t *testing.T) {
	p, err := OpenPolicy(filepath.Join(t.TempDir(), "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := p.Match("agent", "openrouter", "m", now); err != ErrDenied {
		t.Fatalf("empty policy allowed request: %v", err)
	}
	g, err := p.Add(Grant{Agent: "agent", Credential: "c", Provider: "openrouter", Models: []string{"allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Match("agent", "openrouter", "allowed", now); err != nil {
		t.Fatalf("allowed model denied: %v", err)
	}
	if _, err := p.Match("agent", "openrouter", "other", now); err != ErrDenied {
		t.Fatalf("disallowed model not denied: %v", err)
	}
	if _, err := p.Match("agent", "openai", "allowed", now); err != ErrDenied {
		t.Fatalf("provider crossing not denied: %v", err)
	}
	if g.Limits.MaxRequestsPerMinute == 0 || g.Limits.MaxConcurrent == 0 {
		t.Fatalf("default limits not applied: %+v", g.Limits)
	}
}

func TestPolicyWildcardAndExpiry(t *testing.T) {
	p, _ := OpenPolicy(filepath.Join(t.TempDir(), "policy.json"))
	past := time.Now().Add(-time.Hour)
	_, err := p.Add(Grant{Agent: "a", Credential: "c", Provider: "openrouter",
		Models: []string{"*"}, ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Match("a", "openrouter", "anything", time.Now()); err != ErrDenied {
		t.Fatalf("expired grant allowed: %v", err)
	}
	future := time.Now().Add(time.Hour)
	_, err = p.Add(Grant{Agent: "a", Credential: "c", Provider: "openrouter",
		Models: []string{"*"}, ExpiresAt: &future})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Match("a", "openrouter", "anything", time.Now()); err != nil {
		t.Fatalf("wildcard denied: %v", err)
	}
}

func TestPolicyPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	p, _ := OpenPolicy(path)
	g, err := p.Add(Grant{Agent: "a", Credential: "c", Provider: "openai", Models: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := OpenPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p2.Get(g.ID); !ok {
		t.Fatal("grant not persisted")
	}
	if err := p2.Remove("a", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Match("a", "openai", "m", time.Now()); err != ErrDenied {
		t.Fatal("removed grant still matched")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("policy mode = %v, want 0600", fi.Mode().Perm())
	}
}
