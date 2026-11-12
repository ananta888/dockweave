// Package agentconfig materializes per-agent MCP server and skill settings
// into the agent's persistent memory home before the container starts.
//
// Formats:
//   - OpenCode: ~/.config/opencode/opencode.json ("mcp" section)
//   - Claude:   ~/.claude.json ("mcpServers" section)
//   - all:      ~/.dockweave/{mcp,skills,contracts}.json (generic manifest)
//
// Secret VALUES are never stored in the database or written here; only
// environment variable names are preserved. Values belong in the vault.
package agentconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ananta888/dockweave/internal/model"
)

// Write materializes the current configuration into memDir.
func Write(memDir, agentType string, uid, gid int, mcps []*model.AgentMCP, skills []*model.AgentSkill) error {
	if memDir == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(memDir, ".dockweave"), 0o755); err != nil {
		return fmt.Errorf("create agent config dir: %w", err)
	}
	if err := writeGeneric(memDir, mcps, skills); err != nil {
		return err
	}
	switch agentType {
	case "opencode":
		if err := writeOpenCode(memDir, mcps); err != nil {
			return err
		}
	case "claude":
		if err := writeClaude(memDir, mcps); err != nil {
			return err
		}
	}
	if err := writeSkills(memDir, agentType, skills); err != nil {
		return err
	}
	return chownConfig(memDir, uid, gid)
}

func chownConfig(memDir string, uid, gid int) error {
	for _, rel := range []string{".dockweave", ".config", ".claude", ".claude.json"} {
		p := filepath.Join(memDir, rel)
		_ = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chown(path, uid, gid)
			}
			return nil
		})
	}
	return nil
}

func writeGeneric(memDir string, mcps []*model.AgentMCP, skills []*model.AgentSkill) error {
	servers := map[string]any{}
	for _, m := range mcps {
		if !m.Enabled {
			continue
		}
		servers[m.Name] = map[string]any{
			"transport": m.Transport, "command": m.Command, "args": m.Args,
			"url": m.URL, "env_keys": m.EnvKeys, "contract": m.Contract,
		}
		if m.Contract != "" {
			path := filepath.Join(memDir, ".dockweave", "contracts")
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			_ = os.WriteFile(filepath.Join(path, m.Name+".json"), []byte(m.Contract), 0o644)
		}
	}
	manifest := map[string]any{"mcpServers": servers, "skills": skills}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.MkdirAll(filepath.Join(memDir, ".dockweave"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(memDir, ".dockweave", "config.json"), append(b, '\n'), 0o644)
}

func writeOpenCode(memDir string, mcps []*model.AgentMCP) error {
	path := filepath.Join(memDir, ".config", "opencode", "opencode.json")
	cfg, err := readJSONMap(path)
	if err != nil {
		return err
	}
	mcp := map[string]any{}
	for _, m := range mcps {
		if !m.Enabled {
			continue
		}
		if m.Transport == model.MCPStdio {
			cmd := append([]string{m.Command}, m.Args...)
			mcp[m.Name] = map[string]any{"type": "local", "command": cmd, "enabled": true}
		} else {
			mcp[m.Name] = map[string]any{"type": "remote", "url": m.URL, "enabled": true}
		}
	}
	cfg["mcp"] = mcp
	return writeJSONMap(path, cfg)
}

func writeClaude(memDir string, mcps []*model.AgentMCP) error {
	path := filepath.Join(memDir, ".claude.json")
	cfg, err := readJSONMap(path)
	if err != nil {
		return err
	}
	servers := map[string]any{}
	for _, m := range mcps {
		if !m.Enabled {
			continue
		}
		if m.Transport == model.MCPStdio {
			servers[m.Name] = map[string]any{
				"command": m.Command, "args": m.Args,
				"env": map[string]string{},
			}
		} else {
			servers[m.Name] = map[string]any{"type": "http", "url": m.URL}
		}
	}
	cfg["mcpServers"] = servers
	return writeJSONMap(path, cfg)
}

func writeSkills(memDir, agentType string, skills []*model.AgentSkill) error {
	enabled := make([]*model.AgentSkill, 0, len(skills))
	for _, s := range skills {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Name < enabled[j].Name })
	for _, s := range enabled {
		base := filepath.Join(memDir, ".dockweave", "skills", s.Name)
		if agentType == "opencode" {
			base = filepath.Join(memDir, ".config", "opencode", "skills", s.Name)
		} else if agentType == "claude" {
			base = filepath.Join(memDir, ".claude", "skills", s.Name)
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return err
		}
		body := "# " + s.Name + "\n\n" + s.Description + "\n"
		if s.SourcePath != "" {
			body += "\nSource: `" + s.SourcePath + "`\n"
		}
		if s.Config != "" {
			body += "\n```json\n" + s.Config + "\n```\n"
		}
		if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readJSONMap(path string) (map[string]any, error) {
	cfg := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func writeJSONMap(path string, cfg map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
