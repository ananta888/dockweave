//go:build integration
// +build integration

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/egress"
)

// TestEgressTableRoundTrip verifies the nftables manager applies, detects and
// removes its table in a privileged environment. It uses a non-existent bridge
// interface so no real traffic is ever affected, and a unique table name so it
// never collides with the production table.
func TestEgressTableRoundTrip(t *testing.T) {
	requireRoot(t)
	requireHostFirewall(t)
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

// TestEgressTableRulesInKernel applies the table and verifies the drop rules
// (IMDS, private ranges, DNS, IPv6) are actually present in the kernel's
// nftables ruleset, not just generated as a string.
//
// The full bypass matrix (a broker-only container must not reach a fake
// provider on the gateway, must reach the broker port, and direct
// curl/Python/Node plus DNS/IMDS/IPv6 must all fail) additionally requires a
// running broker, a fake provider and an agent container on the bridge — see
// docs/egress-security.md.
func TestEgressTableRulesInKernel(t *testing.T) {
	requireRoot(t)
	requireHostFirewall(t)
	m := egress.New(egress.Config{Table: "dockweave-egress-test", BrokerPort: 8787})
	t.Cleanup(func() { _ = m.Remove(context.Background()) })
	if err := m.Ensure(context.Background(), "br-nonexistent", []string{"172.30.0.1"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	out, err := exec.Command("nft", "list", "table", "inet", "dockweave-egress-test").CombinedOutput()
	if err != nil {
		t.Fatalf("list table: %v: %s", err, out)
	}
	s := string(out)
	for _, want := range []string{
		"169.254.0.0/16", // IMDS/link-local
		"10.0.0.0/8",     // RFC1918
		"dport 8787",     // broker port accept
		"dport 53",       // DNS drop
	} {
		if !strings.Contains(s, want) {
			t.Errorf("kernel ruleset missing %q\n%s", want, s)
		}
	}
}
