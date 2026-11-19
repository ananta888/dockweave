package egress

import (
	"strings"
	"testing"
)

func TestTableContainsCoreRules(t *testing.T) {
	m := New(Config{
		BrokerPort: 8787,
	})
	script := m.Table("br-dockweave", []string{"172.30.0.1"})

	for _, want := range []string{
		"table inet dockweave-egress {",
		"iifname \"br-dockweave\" jump dw-broker-only",
		"ip daddr @broker_addrs tcp dport 8787 accept",
		"ip daddr 169.254.0.0/16", // IMDS/link-local
		"ip daddr 10.0.0.0/8",     // RFC1918
		"ip daddr 172.16.0.0/12",  // RFC1918
		"ip daddr 192.168.0.0/16", // RFC1918
		"ip6 daddr fe80::/10",     // IPv6 link-local
		"ip6 daddr fc00::/7",      // IPv6 ULA
		"udp dport 53 drop",       // DNS
		"tcp dport 53 drop",       // DNS
		"drop",                    // default deny for bridge traffic
	} {
		if !strings.Contains(script, want) {
			t.Errorf("table script missing %q\n---\n%s", want, script)
		}
	}
}

func TestTableDefaults(t *testing.T) {
	m := New(Config{})
	script := m.Table("", nil)
	if !strings.Contains(script, "table inet dockweave-egress {") {
		t.Fatalf("default table name not applied: %s", script)
	}
	if !strings.Contains(script, "tcp dport 8787 accept") {
		t.Fatalf("default broker port 8787 not applied: %s", script)
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"b", "a", "b", " a ", ""})
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dedupe = %v, want %v", got, want)
	}
}

func TestBlockedPrefixesAreOrdered(t *testing.T) {
	p := blockedPrefixes()
	if len(p) < 8 {
		t.Fatalf("expected at least 8 blocked prefixes, got %d", len(p))
	}
}
