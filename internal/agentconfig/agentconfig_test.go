package agentconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestWriteOpenCodeAndClaude(t *testing.T) {
	dir := t.TempDir()
	mcps := []*model.AgentMCP{{Name: "fs", Transport: model.MCPStdio, Command: "npx", Args: []string{"-y", "pkg"}, Enabled: true}}
	skills := []*model.AgentSkill{{Name: "review", Description: "review code", Enabled: true, CreatedAt: time.Now()}}
	if err := Write(dir, "opencode", 0, 0, mcps, skills); err != nil {
		t.Fatal(err)
	}
	oc := filepath.Join(dir, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(oc); err != nil {
		t.Fatalf("opencode config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".config", "opencode", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("opencode skill missing: %v", err)
	}
	if err := Write(dir, "claude", 0, 0, mcps, skills); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err != nil {
		t.Fatalf("claude config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".dockweave", "config.json")); err != nil {
		t.Fatalf("generic manifest missing: %v", err)
	}
}
