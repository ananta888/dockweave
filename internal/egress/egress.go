// Package egress implements host-side egress enforcement for credential-backed
// agents. Docker's internal network already blocks external routes, but a
// broker-only agent can still reach any *host* service bound to the network
// gateway (not just the broker). This package generates and applies a
// dedicated nftables table that restricts exactly that surface.
//
// The table is declarative and idempotent; applying it is fail-closed: if the
// `nft` binary is missing, the process lacks privileges, or applying fails, an
// error is returned and the caller must refuse the agent start.
package egress

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Config describes the host-side egress firewall.
type Config struct {
	// Table is the nftables table name. Default "dockweave-egress".
	Table string
	// BrokerPort is the TCP port the broker listens on at the gateway.
	BrokerPort uint16
	// BrokerAddrs are the gateway addresses (IPv4) the broker may be reached
	// at. Traffic to any other address/port on the gateway is dropped.
	BrokerAddrs []string
	// Interface is the host bridge interface of the broker-only network
	// (e.g. "br-<id>"). Only traffic arriving on this interface is filtered.
	Interface string
}

// DefaultTable is the nftables table owned by DockWeave.
const DefaultTable = "dockweave-egress"

// Manager applies and removes the egress table via the `nft` binary.
type Manager struct {
	cfg Config
}

// New builds a Manager. Config is normalized (defaults applied).
func New(cfg Config) *Manager {
	if cfg.Table == "" {
		cfg.Table = DefaultTable
	}
	if cfg.BrokerPort == 0 {
		cfg.BrokerPort = 8787
	}
	cfg.BrokerAddrs = dedupe(cfg.BrokerAddrs)
	return &Manager{cfg: cfg}
}

// Table returns the declarative nftables script for the given network
// interface and broker gateway addresses. It is pure and unit-testable.
func (m *Manager) Table(iface string, addrs []string) string {
	cfg := m.cfg
	cfg.Interface = iface
	cfg.BrokerAddrs = dedupe(addrs)
	return tableScript(cfg)
}

// Ensure applies the table for the given broker-only network interface and
// gateway addresses if it is not already present. It is fail-closed: any
// inability to apply the rules is returned as an error.
func (m *Manager) Ensure(ctx context.Context, iface string, addrs []string) error {
	present, err := m.Present(ctx)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("nftables egress enforcement requires root; refusing (fail closed)")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("nft binary not found; cannot enforce egress (fail closed)")
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(m.Table(iface, addrs))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply nftables table %q: %v: %s", m.cfg.Table, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Present reports whether the table currently exists in the kernel.
func (m *Manager) Present(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("nft"); err != nil {
		return false, errors.New("nft binary not found")
	}
	cmd := exec.CommandContext(ctx, "nft", "list", "table", "inet", m.cfg.Table)
	if err := cmd.Run(); err != nil {
		// "No such file or directory" / non-zero exit means the table is absent.
		return false, nil
	}
	return true, nil
}

// Remove flushes and deletes the table (used by tests and teardown).
func (m *Manager) Remove(ctx context.Context) error {
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("nft binary not found")
	}
	cmd := exec.CommandContext(ctx, "nft", "delete", "table", "inet", m.cfg.Table)
	// Deleting a non-existent table is an error; treat it as already gone.
	_ = cmd.Run()
	return nil
}

// tableScript renders the nftables table. The policy: traffic arriving on the
// broker-only bridge is allowed only if it is (a) an established/related
// reply, or (b) TCP to the broker port on an allowed gateway address. Private
// ranges, link-local/metadata (IMDS), IPv6 ULA/link-local/multicast and DNS
// are dropped explicitly; everything else arriving from the bridge is dropped.
func tableScript(cfg Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", cfg.Table)
	if len(cfg.BrokerAddrs) > 0 {
		fmt.Fprintf(&b, "  set broker_addrs {\n    type ipv4_addr\n    elements = { %s }\n  }\n",
			strings.Join(cfg.BrokerAddrs, ", "))
	}
	b.WriteString("  chain input {\n")
	b.WriteString("    type filter hook input priority 0; policy accept;\n")
	if cfg.Interface != "" {
		fmt.Fprintf(&b, "    iifname %q jump dw-broker-only\n", cfg.Interface)
	} else {
		b.WriteString("    jump dw-broker-only\n")
	}
	b.WriteString("  }\n\n")
	b.WriteString("  chain dw-broker-only {\n")
	b.WriteString("    ct state established,related accept\n")
	if len(cfg.BrokerAddrs) > 0 {
		fmt.Fprintf(&b, "    ip daddr @broker_addrs tcp dport %d accept\n", cfg.BrokerPort)
	} else {
		fmt.Fprintf(&b, "    tcp dport %d accept\n", cfg.BrokerPort)
	}
	// Explicit drops for metadata/IMDS, private, link-local and IPv6 ranges.
	for _, cidr := range blockedPrefixes() {
		fmt.Fprintf(&b, "    %s drop\n", cidr)
	}
	// DNS must not leak to the gateway (the broker does its own resolution).
	b.WriteString("    udp dport 53 drop\n")
	b.WriteString("    tcp dport 53 drop\n")
	// Default: block everything else arriving from the broker-only bridge.
	b.WriteString("    drop\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// blockedPrefixes returns the address prefixes that must never be reachable
// from a broker-only agent via the host gateway.
func blockedPrefixes() []string {
	return []string{
		"ip daddr 169.254.0.0/16", // link-local incl. IMDS 169.254.169.254
		"ip daddr 10.0.0.0/8",     // RFC1918
		"ip daddr 172.16.0.0/12",  // RFC1918
		"ip daddr 192.168.0.0/16", // RFC1918
		"ip daddr 100.64.0.0/10",  // CGNAT
		"ip daddr 127.0.0.0/8",    // loopback (should not be routable anyway)
		"ip daddr 224.0.0.0/4",    // multicast
		"ip6 daddr fe80::/10",     // IPv6 link-local
		"ip6 daddr fc00::/7",      // IPv6 ULA
		"ip6 daddr ff00::/8",      // IPv6 multicast
		"ip6 daddr ::1/128",       // IPv6 loopback
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
