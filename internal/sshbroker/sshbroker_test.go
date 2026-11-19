package sshbroker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestCARoundTrip(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := ca.MarshalPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCA(pemBytes)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	if !keysEqual(ca.PublicKey(), loaded.PublicKey()) {
		t.Fatal("public key mismatch after round trip")
	}
	if !strings.Contains(string(pemBytes), "PRIVATE KEY") {
		t.Fatalf("unexpected PEM: %s", pemBytes)
	}
}

func TestPolicyDenyByDefault(t *testing.T) {
	p := NewPolicy()
	if _, ok := p.Match("a", "github.com", "git"); ok {
		t.Fatal("empty policy must deny")
	}
}

func TestPolicyMatchExact(t *testing.T) {
	p := NewPolicy()
	if err := p.Add(Grant{Agent: "opencode-01", Host: "github.com", User: "git", TTL: "10m"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Match("opencode-01", "github.com", "git"); !ok {
		t.Fatal("matching grant denied")
	}
	// Wrong agent, host or user must be denied (no implicit wildcard).
	for _, bad := range [][3]string{
		{"other", "github.com", "git"},
		{"opencode-01", "gitlab.com", "git"},
		{"opencode-01", "github.com", "root"},
	} {
		if _, ok := p.Match(bad[0], bad[1], bad[2]); ok {
			t.Fatalf("unexpected allow for %v", bad)
		}
	}
}

func TestPolicyRejectsBadTTL(t *testing.T) {
	p := NewPolicy()
	if err := p.Add(Grant{Agent: "a", Host: "h", User: "u", TTL: "notaduration"}); err == nil {
		t.Fatal("invalid TTL accepted")
	}
}

func TestIssueCertProperties(t *testing.T) {
	ca, _ := GenerateCA()
	issued, err := Issue(ca, IssueRequest{Agent: "opencode-01", Host: "github.com", User: "git"})
	if err != nil {
		t.Fatal(err)
	}
	c := issued.Cert
	if c.CertType != ssh.UserCert {
		t.Fatalf("cert type = %v, want UserCert", c.CertType)
	}
	if len(c.ValidPrincipals) != 1 || c.ValidPrincipals[0] != "git" {
		t.Fatalf("principals = %v, want [git]", c.ValidPrincipals)
	}
	if c.KeyId != "opencode-01" {
		t.Fatalf("key id = %q", c.KeyId)
	}
	now := time.Now().Unix()
	if int64(c.ValidAfter) > now || int64(c.ValidBefore) <= now {
		t.Fatalf("validity window wrong: after=%d before=%d now=%d", c.ValidAfter, c.ValidBefore, now)
	}
	if int64(c.ValidBefore)-now > int64((DefaultTTL + 2*time.Minute).Seconds()) {
		t.Fatalf("certificate TTL exceeds default: %d", c.ValidBefore-uint64(now))
	}
	if !keysEqual(c.SignatureKey, ca.PublicKey()) {
		t.Fatal("certificate not signed by the CA")
	}
	// The CertPEM must re-parse as the same certificate.
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(issued.CertPEM)
	if err != nil {
		t.Fatalf("parse CertPEM: %v", err)
	}
	if pc, ok := parsed.(*ssh.Certificate); !ok || !keysEqual(pc.SignatureKey, ca.PublicKey()) {
		t.Fatal("CertPEM does not re-parse to a CA-signed certificate")
	}
}

func TestBrokerEnforcesPolicy(t *testing.T) {
	ca, _ := GenerateCA()
	p := NewPolicy()
	_ = p.Add(Grant{Agent: "a", Host: "github.com", User: "git"})
	b := New(ca, p)

	if _, err := b.Issue("a", "github.com", "git", time.Minute); err != nil {
		t.Fatalf("granted issuance failed: %v", err)
	}
	if _, err := b.Issue("a", "gitlab.com", "git", time.Minute); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied issuance error = %v, want ErrDenied", err)
	}
}

func TestIssueMaxTTL(t *testing.T) {
	ca, _ := GenerateCA()
	issued, err := Issue(ca, IssueRequest{Agent: "a", Host: "h", User: "u", TTL: 48 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// The certificate must expire within MaxTTL of now (allowing a small
	// slack; the validity window also carries a 5-minute clock-skew backoff).
	if int64(issued.Cert.ValidBefore)-now > int64((MaxTTL + time.Minute).Seconds()) {
		t.Fatalf("certificate exceeds MaxTTL: before=%d now=%d", issued.Cert.ValidBefore, now)
	}
}
