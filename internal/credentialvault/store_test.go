package credentialvault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestVault(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.json")
	s, err := Create(path, "correct horse battery")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestPutGetListDelete(t *testing.T) {
	s, _ := newTestVault(t)
	if err := s.Put("openrouter-main", "openrouter", "sk-or-secret"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	sec, err := s.Get("openrouter-main")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sec.Value != "sk-or-secret" || sec.Provider != "openrouter" {
		t.Fatalf("unexpected secret: %+v", sec)
	}
	list := s.List()
	if len(list) != 1 || list[0].Name != "openrouter-main" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if err := s.Delete("openrouter-main"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("openrouter-main"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestReopenWithPassphrase(t *testing.T) {
	s, path := newTestVault(t)
	if err := s.Put("k", "openai", "value-123"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, "correct horse battery")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer opened.Close()
	sec, err := opened.Get("k")
	if err != nil || sec.Value != "value-123" {
		t.Fatalf("Get after reopen: %+v %v", sec, err)
	}
}

func TestWrongPassphrase(t *testing.T) {
	s, path := newTestVault(t)
	_ = s.Put("k", "openai", "value")
	_ = s.Close()
	if _, err := Open(path, "wrong passphrase"); err == nil {
		t.Fatal("expected error for wrong passphrase")
	}
}

func TestNoPlaintextOnDisk(t *testing.T) {
	s, path := newTestVault(t)
	secret := "sk-or-very-distinctive-secret-value"
	if err := s.Put("openrouter", "openrouter", secret); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(secret)) {
		t.Fatal("plaintext secret found in vault file")
	}
	if bytes.Contains(b, []byte("correct horse battery")) {
		t.Fatal("passphrase found in vault file")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("vault mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestTamperDetected(t *testing.T) {
	s, path := newTestVault(t)
	_ = s.Put("k", "openai", "value")
	_ = s.Close()
	b, _ := os.ReadFile(path)
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	e := env.Credentials["k"]
	ct, err := base64.StdEncoding.DecodeString(e.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	ct[0] ^= 0xff
	e.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	env.Credentials["k"] = e
	b, _ = json.Marshal(env)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path, "correct horse battery")
	if err != nil {
		return // sentinel tamper is also a valid detection
	}
	defer opened.Close()
	if _, err := opened.Get("k"); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestCiphertextBoundToName(t *testing.T) {
	s, _ := newTestVault(t)
	_ = s.Put("a", "openai", "same-value")
	_ = s.Put("b", "openai", "same-value")
	// Swapping entries must fail because AAD includes the name.
	s.mu.Lock()
	ea, eb := s.env.Credentials["a"], s.env.Credentials["b"]
	ea.Nonce, ea.Ciphertext, eb.Nonce, eb.Ciphertext =
		eb.Nonce, eb.Ciphertext, ea.Nonce, ea.Ciphertext
	s.env.Credentials["a"], s.env.Credentials["b"] = ea, eb
	s.mu.Unlock()
	if _, err := s.Get("a"); err == nil {
		t.Fatal("entry swap was accepted; AAD binding failed")
	}
}

func TestRotatePassphrase(t *testing.T) {
	s, path := newTestVault(t)
	if err := s.Put("k", "openai", "value-rotate"); err != nil {
		t.Fatal(err)
	}
	if err := s.RotatePassphrase("new passphrase 123"); err != nil {
		t.Fatalf("RotatePassphrase: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "correct horse battery"); err == nil {
		t.Fatal("old passphrase still works after rotation")
	}
	opened, err := Open(path, "new passphrase 123")
	if err != nil {
		t.Fatalf("Open with new passphrase: %v", err)
	}
	defer opened.Close()
	sec, err := opened.Get("k")
	if err != nil || sec.Value != "value-rotate" {
		t.Fatalf("secret lost after rotation: %+v %v", sec, err)
	}
}

func TestLockPreventsSecondProcess(t *testing.T) {
	s, path := newTestVault(t)
	_ = s.Put("k", "openai", "v")
	if _, err := Open(path, "correct horse battery"); err != ErrLocked {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	_ = s.Close()
}

func TestCreateRejectsExisting(t *testing.T) {
	_, path := newTestVault(t)
	if _, err := Create(path, "another passphrase"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}
