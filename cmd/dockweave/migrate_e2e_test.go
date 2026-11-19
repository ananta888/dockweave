package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/brokerclient"
	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/credentialvault"
)

// startBrokerAdmin spins up a real broker admin handler over a unix socket
// backed by a fresh in-memory vault/policy/sessions/budget. This exercises the
// exact HTTP + SO_PEERCRED path the `secret migrate` command uses.
func startBrokerAdmin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	vault, err := credentialvault.Create(filepath.Join(dir, "vault.json"), "test-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	policy, err := credentialbroker.OpenPolicy(filepath.Join(dir, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := credentialbroker.OpenSessions(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	budget, err := credentialbroker.OpenBudget(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := credentialbroker.NewAdminHandler(credentialbroker.AdminConfig{
		Vault: vault, Policy: policy, Sessions: sessions, Budget: budget,
		AllowedUIDs: []uint32{uint32(os.Geteuid())},
	})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "admin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: admin, ConnContext: credentialbroker.PeerConnContext}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return sock
}

// TestMigrateStoreRoundTrip proves the migration storage path end-to-end:
// candidate detection from an env file, secret store into the vault and grant
// creation via the real admin HTTP client. Values never come back.
func TestMigrateStoreRoundTrip(t *testing.T) {
	sock := startBrokerAdmin(t)
	c := brokerclient.New(sock)

	content := "OPENAI_API_KEY=sk-test-secret\n"
	cands, _ := planEnvMigration(content, "dev", "")
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v, want 1", cands)
	}
	e := cands[0]
	value := envValue(content, e.Key)
	if value != "sk-test-secret" {
		t.Fatalf("envValue = %q", value)
	}

	if err := c.PutSecret(context.Background(), e.Name, e.Provider, value); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	g, err := c.AddGrant(context.Background(), credentialbroker.Grant{
		Agent: "dev", Credential: e.Name, Models: []string{"*"},
	})
	if err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	if g.ID == "" {
		t.Fatal("empty grant id")
	}

	secrets, err := c.ListSecrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range secrets {
		if m.Name == e.Name && m.Provider == "openai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("secret %q not listed after migration", e.Name)
	}
	grants, err := c.ListGrants(context.Background(), "dev")
	if err != nil || len(grants) != 1 || grants[0].Credential != e.Name {
		t.Fatalf("grants = %+v (err %v)", grants, err)
	}
}

// TestMigrateRejectsUnknownKey verifies the migration planner surfaces unknown
// keys as skipped instead of silently storing them (fail-safe on gaps).
func TestMigrateRejectsUnknownKey(t *testing.T) {
	cands, skipped := planEnvMigration("SOME_RANDOM_VAR=abc\nOPENAI_API_KEY=sk\n", "dev", "")
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}
	if len(skipped) != 1 || skipped[0] != "SOME_RANDOM_VAR" {
		t.Fatalf("skipped = %v", skipped)
	}
}
