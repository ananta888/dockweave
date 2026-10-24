package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRolesAndContainerDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dockweaved.conf")
	body := `
# test config
ADMIN_UIDS=0:1000
MONITOR_UIDS=2000
AGENT_UID=1337
AGENT_GID=1338
ALLOW_ROOT_CONTAINER=false
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AdminUIDs) != 2 || cfg.AdminUIDs[0] != 0 || cfg.AdminUIDs[1] != 1000 {
		t.Fatalf("admin uids = %v", cfg.AdminUIDs)
	}
	if len(cfg.MonitorUIDs) != 1 || cfg.MonitorUIDs[0] != 2000 {
		t.Fatalf("monitor uids = %v", cfg.MonitorUIDs)
	}
	if cfg.AgentUID != 1337 || cfg.AgentGID != 1338 {
		t.Fatalf("agent identity = %d:%d", cfg.AgentUID, cfg.AgentGID)
	}
	if cfg.AllowRootContainer {
		t.Fatal("root containers must be opt-in")
	}
}

func TestDefaultsAreNonRootAndAdminOnlyRoot(t *testing.T) {
	cfg := Default()
	if cfg.AgentUID != 1000 || cfg.AgentGID != 1000 {
		t.Fatalf("default identity = %d:%d, want 1000:1000", cfg.AgentUID, cfg.AgentGID)
	}
	if cfg.AllowRootContainer {
		t.Fatal("root containers must be opt-in")
	}
	if len(cfg.AdminUIDs) != 1 || cfg.AdminUIDs[0] != 0 {
		t.Fatalf("default admin uids = %v, want [0]", cfg.AdminUIDs)
	}
	if len(cfg.MonitorUIDs) != 0 {
		t.Fatalf("default monitor uids = %v, want none", cfg.MonitorUIDs)
	}
}
