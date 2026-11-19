//go:build integration
// +build integration

package integration

import (
	"context"
	"os/exec"
	"testing"

	"github.com/ananta888/dockweave/internal/egress"
)

// TestEgressTableRoundTrip verifies the nftables manager applies, detects and
// removes its table in a privileged environment. It uses a non-existent bridge
// interface so no real traffic is ever affected, and a unique table name so it
// never collides with the production table.
func TestEgressTableRoundTrip(t *testing.T) {
	requireRoot(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft binary not available")
	}
	m := egress.New(egress.Config{Table: "dockweave-egress-test", BrokerPort: 8787})
	t.Cleanup(func() { _ = m.Remove(context.Background()) })

	if err := m.Ensure(context.Background(), "br-nonexistent", []string{"172.30.0.1"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	present, err := m.Present(context.Background())
	if err != nil || !present {
		t.Fatalf("present = %v, err = %v; want true, nil", present, err)
	}
	// Idempotent: a second Ensure is a no-op.
	if err := m.Ensure(context.Background(), "br-nonexistent", []string{"172.30.0.1"}); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if err := m.Remove(context.Background()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	present, _ = m.Present(context.Background())
	if present {
		t.Fatal("table still present after Remove")
	}
}
