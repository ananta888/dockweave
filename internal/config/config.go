// Package config loads daemon configuration from file and environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds daemon settings.
type Config struct {
	Root         string   // state root, default /var/lib/dockweave
	SocketPath   string   // unix socket, default /run/dockweave/dockweaved.sock
	DBPath       string   // sqlite path, default <root>/dockweave.db
	AllowedRoots []string // additional allowed mount source roots

	// AdminUIDs may use the full API (mounts, grants, mutations).
	// MonitorUIDs get read-only access. Unknown UIDs are denied.
	AdminUIDs   []uint32
	MonitorUIDs []uint32
	// GatewayUIDs may forward the signed-in human to the daemon (must be
	// admin peers). Ideally the gateway runs as a dedicated user.
	GatewayUIDs []uint32

	// AgentUID/AgentGID is the container identity used when an agent has
	// no explicit UID (review-005: non-root containers by default).
	AgentUID int
	AgentGID int
	// AllowRootContainer permits UID 0 containers (explicitly configured;
	// the daemon audits every root container).
	AllowRootContainer bool

	// KnowledgeDir is the shared, human-readable knowledge store mounted
	// read-write into every agent at /knowledge (review follow-up).
	KnowledgeDir string

	// SocketGroup is the group that may connect to the unix sockets
	// (mode 0660, owner root). Empty keeps root:root; "dockweave" is the
	// default so an admin group can be created without config changes.
	// Roles still apply on top of socket access.
	SocketGroup string

	// MountdSocket routes privileged mount syscalls to dockweave-mountd so
	// the daemon itself can run without CAP_SYS_ADMIN.
	MountdSocket string

	// BrokerAdminSocket is the unix socket of the credential broker's
	// operator API. Empty disables broker metadata endpoints. The daemon
	// must be in the broker's admin UID allowlist (root usually is not by
	// default; configure BROKER_ADMIN_UIDS=0 with --admin-uids).
	BrokerAdminSocket string

	// EgressNftables enables host-side nftables enforcement for broker-only
	// agents (egress-003). Disabled by default; when enabled, the daemon
	// applies a DockWeave-owned nftables table and refuses the agent start
	// (fail closed) if it cannot.
	EgressNftables bool

	// Ticketing (ADR 0003): primary provider (native|paperclip; empty =
	// ticketing off), optional mirror and Paperclip connection.
	TicketProvider     string
	TicketMirror       string
	PaperclipURL       string
	PaperclipCompany   string
	PaperclipTokenFile string
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Root:       "/var/lib/dockweave",
		SocketPath: "/run/dockweave/dockweaved.sock",
		DBPath:     "/var/lib/dockweave/dockweave.db",
		AllowedRoots: []string{
			"/srv", "/home", "/opt/projects", "/var/lib/dockweave/shared",
		},
		AdminUIDs:          []uint32{0},
		MonitorUIDs:        nil,
		AgentUID:           1000,
		AgentGID:           1000,
		AllowRootContainer: false,
		SocketGroup:        "dockweave",
		KnowledgeDir:       "/var/lib/dockweave/knowledge",
	}
}

// Load reads a simple KEY=VALUE config file (if it exists) and applies
// environment overrides (DOCKWEAVE_*).
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config %q: %w", path, err)
		}
		applyPairs(&cfg, strings.Split(string(b), "\n"))
	}
	// Environment overrides.
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "DOCKWEAVE_") {
			env = append(env, strings.TrimPrefix(e, "DOCKWEAVE_"))
		}
	}
	applyPairs(&cfg, env)
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.Root, "dockweave.db")
	}
	return cfg, nil
}

func applyPairs(cfg *Config, lines []string) {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "ROOT":
			cfg.Root = v
		case "SOCKET_PATH":
			cfg.SocketPath = v
		case "SOCKET_GROUP":
			cfg.SocketGroup = v
		case "MOUNTD_SOCKET":
			cfg.MountdSocket = v
		case "BROKER_ADMIN_SOCKET":
			cfg.BrokerAdminSocket = v
		case "DB_PATH":
			cfg.DBPath = v
		case "ALLOWED_ROOTS":
			cfg.AllowedRoots = strings.Split(v, ":")
		case "ADMIN_UIDS":
			cfg.AdminUIDs = parseUIDList(v)
		case "MONITOR_UIDS":
			cfg.MonitorUIDs = parseUIDList(v)
		case "GATEWAY_UIDS":
			cfg.GatewayUIDs = parseUIDList(v)
		case "AGENT_UID":
			fmt.Sscanf(v, "%d", &cfg.AgentUID)
		case "AGENT_GID":
			fmt.Sscanf(v, "%d", &cfg.AgentGID)
		case "ALLOW_ROOT_CONTAINER":
			cfg.AllowRootContainer = v == "true" || v == "1"
		case "KNOWLEDGE_DIR":
			cfg.KnowledgeDir = v
		case "EGRESS_NFTABLES":
			cfg.EgressNftables = v == "true" || v == "1"
		case "TICKET_PROVIDER":
			cfg.TicketProvider = strings.ToLower(v)
		case "TICKET_MIRROR":
			cfg.TicketMirror = strings.ToLower(v)
		case "PAPERCLIP_URL":
			cfg.PaperclipURL = v
		case "PAPERCLIP_COMPANY":
			cfg.PaperclipCompany = v
		case "PAPERCLIP_TOKEN_FILE":
			cfg.PaperclipTokenFile = v
		}
	}
}

// parseUIDList parses a colon-separated list of numeric UIDs.
func parseUIDList(v string) []uint32 {
	var out []uint32
	for _, p := range strings.Split(v, ":") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var n uint32
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}
