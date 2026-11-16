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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ananta888/dockweave/internal/model"
)

// openCodeNativeScript starts the shared OpenCode server and pins the TUI to
// the session that the daemon's native backend drives over HTTP.
const openCodeNativeScript = `#!/bin/sh
set -eu
BIN="$HOME/.local/bin/opencode"
rm -f "$HOME/.local/bin/opencode2"
npm install -g --prefix "$HOME/.local" @opencode/cli@2.2.6
"$BIN" service set hostname 127.0.0.1 || true
"$BIN" service set port 4096 || true
"$BIN" service start || "$BIN" service restart || true
i=0
while [ "$i" -lt 100 ]; do
  if "$BIN" api GET /api/health >/dev/null 2>&1; then break; fi
  i=$((i + 1))
  sleep 0.2
done
SID=""
if [ -f "$HOME/.dockweave/opencode-session" ]; then
  SID=$(cat "$HOME/.dockweave/opencode-session")
fi
if [ -z "$SID" ]; then
  SID=$("$BIN" api POST /api/session --data "{\"title\":\"dockweave\",\"location\":{\"directory\":\"$PWD\"}}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["id"])')
  mkdir -p "$HOME/.dockweave"
  printf '%s' "$SID" > "$HOME/.dockweave/opencode-session"
fi
exec "$BIN" --session "$SID" --auto
`

// codexNativeScript resumes a stable Codex thread so the TUI and the daemon's
// `codex queue` path share the same session.
const codexNativeScript = `#!/bin/sh
set -eu
BIN="$HOME/.local/bin/codex"
npm install -g --prefix "$HOME/.local" @openai/codex
THREAD_FILE="$HOME/.dockweave/codex-thread"
THREAD=""
if [ -f "$THREAD_FILE" ]; then
  THREAD=$(cat "$THREAD_FILE")
fi
if [ -z "$THREAD" ]; then
  NEWEST=$(ls -t "$HOME"/.codex/sessions/*/*/*/*.jsonl 2>/dev/null | head -1 || true)
  if [ -n "$NEWEST" ]; then
    THREAD=$(printf '%s' "$(basename "$NEWEST")" | grep -oE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}')
    mkdir -p "$HOME/.dockweave"
    printf '%s' "$THREAD" > "$THREAD_FILE"
  fi
fi
if [ -n "$THREAD" ]; then
  exec "$BIN" resume "$THREAD" --dangerously-bypass-approvals-and-sandbox
fi
exec "$BIN" --dangerously-bypass-approvals-and-sandbox
`

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
	if agentType == "opencode" {
		p := filepath.Join(memDir, ".dockweave", "opencode-native.sh")
		if err := os.WriteFile(p, []byte(openCodeNativeScript), 0o755); err != nil {
			return err
		}
	}
	if agentType == "codex" {
		p := filepath.Join(memDir, ".dockweave", "codex-native.sh")
		if err := os.WriteFile(p, []byte(codexNativeScript), 0o755); err != nil {
			return err
		}
	}
	return chownConfig(memDir, uid, gid)
}

func chownConfig(memDir string, uid, gid int) error {
	for _, rel := range []string{".dockweave", ".config", ".claude", ".claude.json", ".codex", ".gemini"} {
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
		if !safeSkillName(s.Name) {
			continue
		}
		base := filepath.Join(memDir, ".dockweave", "skills", s.Name)
		switch agentType {
		case "opencode":
			base = filepath.Join(memDir, ".config", "opencode", "skills", s.Name)
		case "claude":
			base = filepath.Join(memDir, ".claude", "skills", s.Name)
		case "codex":
			base = filepath.Join(memDir, ".codex", "skills", s.Name)
		case "gemini", "antigravity":
			base = filepath.Join(memDir, ".gemini", "skills", s.Name)
		}
		// A skill with a source folder is copied including references,
		// scripts and assets; a metadata-only entry keeps the legacy stub.
		if copySkillTree(s.SourcePath, base) == nil {
			continue
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

func safeSkillName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return false
	}
	return true
}

// copySkillTree copies a skill folder into dst, replacing any previous copy.
// It only copies regular files and directories (no symlinks) and refuses paths
// that escape dst.
func copySkillTree(src, dst string) error {
	if src == "" {
		return errors.New("empty source")
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return err
	}
	tmp := dst + ".tmp-skill"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	files, total := 0, int64(0)
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = filepath.Clean(rel)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("skill path escapes source")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		out := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files++
		total += info.Size()
		if files > 800 || total > 32<<20 {
			return errors.New("skill folder too large")
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, mode)
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
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
