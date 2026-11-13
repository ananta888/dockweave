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

func TestWriteCopiesSkillTree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "keycloak")
	if err := os.MkdirAll(filepath.Join(src, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: keycloak\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "references", "foundations.md"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []struct{ agent, dir string }{
		{"opencode", ".config/opencode/skills"},
		{"claude", ".claude/skills"},
		{"codex", ".codex/skills"},
		{"gemini", ".gemini/skills"},
	} {
		dir := t.TempDir()
		sk := []*model.AgentSkill{{Name: "keycloak", SourcePath: src, Enabled: true}}
		if err := Write(dir, typ.agent, 0, 0, nil, sk); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(typ.dir), "keycloak")
		if b, err := os.ReadFile(filepath.Join(target, "references", "foundations.md")); err != nil || string(b) != "ref" {
			t.Errorf("%s: reference not copied: %v", typ.agent, err)
		}
		if b, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(b) != "---\nname: keycloak\n---\nbody" {
			t.Errorf("%s: SKILL.md not the source file: %q %v", typ.agent, b, err)
		}
	}
}

func TestWriteRejectsUnsafeSkillName(t *testing.T) {
	dir := t.TempDir()
	sk := []*model.AgentSkill{{Name: "../escape", Description: "x", Enabled: true}}
	if err := Write(dir, "opencode", 0, 0, nil, sk); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Fatal("unsafe skill name escaped the skills directory")
	}
}

func TestScanFindsSkillsAndMCP(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, ".config", "opencode", "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Review\n\nFind bugs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oc := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.WriteFile(oc, []byte(`{"mcp":{"fs":{"type":"local","command":["npx","-y","pkg"],"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{"remote":{"url":"http://127.0.0.1:3001/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mcps, skills := Scan("opencode", []string{home})
	if len(mcps) != 1 {
		t.Fatalf("opencode mcps = %+v", mcps)
	}
	claudeMCP, _ := Scan("claude", []string{home})
	if len(claudeMCP) != 1 {
		t.Fatalf("claude mcps = %+v", claudeMCP)
	}
	if len(skills) != 1 || skills[0].Name != "review" {
		t.Fatalf("skills = %+v", skills)
	}
}
