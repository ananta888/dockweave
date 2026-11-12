package agentconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ananta888/dockweave/internal/model"
)

// Scan reads existing skills and MCP server configs from the given home
// directories for an agent type. It is intentionally best-effort: unreadable
// or malformed files are skipped, never guessed.
func Scan(agentType string, dirs []string) ([]*model.AgentTypeMCP, []*model.AgentTypeSkill) {
	mcpByName := map[string]*model.AgentTypeMCP{}
	skillByName := map[string]*model.AgentTypeSkill{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, m := range scanMCP(agentType, dir) {
			if _, ok := mcpByName[m.Name]; !ok {
				mcpByName[m.Name] = m
			}
		}
		for _, s := range scanSkills(agentType, dir) {
			if _, ok := skillByName[s.Name]; !ok {
				skillByName[s.Name] = s
			}
		}
	}
	mcps := make([]*model.AgentTypeMCP, 0, len(mcpByName))
	for _, m := range mcpByName {
		mcps = append(mcps, m)
	}
	skills := make([]*model.AgentTypeSkill, 0, len(skillByName))
	for _, s := range skillByName {
		skills = append(skills, s)
	}
	return mcps, skills
}

func scanSkills(agentType, dir string) []*model.AgentTypeSkill {
	var roots []string
	var rels []string
	switch agentType {
	case "opencode":
		rels = []string{".config/opencode/skills", ".opencode/skills", "skills"}
	case "claude":
		rels = []string{".claude/skills"}
	case "codex":
		rels = []string{".codex/skills"}
	case "gemini", "antigravity":
		rels = []string{".gemini/skills"}
	default:
		rels = []string{".dockweave/skills"}
	}
	for _, rel := range rels {
		p := filepath.Join(dir, rel)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			roots = append(roots, p)
		}
	}
	var out []*model.AgentTypeSkill
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == "node_modules" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "SKILL.md" {
				return nil
			}
			name := filepath.Base(filepath.Dir(path))
			if name == "" || name == "." || seen[name] {
				return nil
			}
			seen[name] = true
			out = append(out, &model.AgentTypeSkill{
				Name: name, Description: firstDescription(path),
				SourcePath: filepath.Dir(path), Enabled: true,
			})
			return nil
		})
	}
	return out
}

func firstDescription(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		if len(line) > 240 {
			line = line[:240]
		}
		return line
	}
	return ""
}

func scanMCP(agentType, dir string) []*model.AgentTypeMCP {
	var out []*model.AgentTypeMCP
	seen := map[string]bool{}
	add := func(m *model.AgentTypeMCP) {
		if m == nil || m.Name == "" || seen[m.Name] {
			return
		}
		seen[m.Name] = true
		m.AgentType = agentType
		out = append(out, m)
	}
	switch agentType {
	case "opencode":
		for _, path := range []string{
			filepath.Join(dir, ".config", "opencode", "opencode.json"),
			filepath.Join(dir, "opencode.json"),
		} {
			addAll(add, mcpFromJSON(path, "mcp"))
		}
	case "claude":
		for _, path := range []string{
			filepath.Join(dir, ".claude.json"),
			filepath.Join(dir, ".mcp.json"),
		} {
			addAll(add, mcpFromJSON(path, "mcpServers"))
		}
	case "codex":
		addAll(add, mcpFromCodexTOML(filepath.Join(dir, ".codex", "config.toml")))
	case "gemini", "antigravity":
		addAll(add, mcpFromJSON(filepath.Join(dir, ".gemini", "settings.json"), "mcpServers"))
	default:
		addAll(add, mcpFromJSON(filepath.Join(dir, ".dockweave", "config.json"), "mcpServers"))
	}
	return out
}

func addAll(add func(*model.AgentTypeMCP), list []*model.AgentTypeMCP) {
	for _, m := range list {
		add(m)
	}
}

func mcpFromJSON(path, key string) []*model.AgentTypeMCP {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil
	}
	servers, _ := root[key].(map[string]any)
	var out []*model.AgentTypeMCP
	for name, raw := range servers {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := &model.AgentTypeMCP{Name: name, Enabled: true}
		if v, ok := m["enabled"].(bool); ok {
			entry.Enabled = v
		}
		if v, ok := m["command"].(string); ok {
			entry.Command = v
			entry.Transport = model.MCPStdio
		}
		if arr, ok := m["command"].([]any); ok && len(arr) > 0 {
			entry.Transport = model.MCPStdio
			for i, a := range arr {
				s, _ := a.(string)
				if i == 0 {
					entry.Command = s
				} else {
					entry.Args = append(entry.Args, s)
				}
			}
		}
		if arr, ok := m["args"].([]any); ok {
			entry.Transport = model.MCPStdio
			for _, a := range arr {
				if s, ok := a.(string); ok {
					entry.Args = append(entry.Args, s)
				}
			}
		}
		if v, ok := m["url"].(string); ok && v != "" {
			entry.URL = v
			entry.Transport = model.MCPHTTP
		}
		if t, ok := m["type"].(string); ok {
			switch t {
			case "remote", "http", "sse":
				entry.Transport = model.MCPTransport(t)
				if entry.Transport == "sse" {
					entry.Transport = model.MCPSse
				} else {
					entry.Transport = model.MCPHTTP
				}
			}
		}
		if entry.Transport == "" {
			entry.Transport = model.MCPStdio
		}
		out = append(out, entry)
	}
	return out
}

// mcpFromCodexTOML parses only [mcp_servers.<name>] tables with string,
// array-of-string and bool values. This covers the common Codex config shape
// without pulling a TOML library into the daemon.
func mcpFromCodexTOML(path string) []*model.AgentTypeMCP {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []*model.AgentTypeMCP
	current := (*model.AgentTypeMCP)(nil)
	flush := func() {
		if current != nil && current.Name != "" {
			if current.Transport == "" {
				current.Transport = model.MCPStdio
			}
			out = append(out, current)
		}
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			current = nil
			section := strings.Trim(line, "[]")
			if strings.HasPrefix(section, "mcp_servers.") {
				name := strings.TrimPrefix(section, "mcp_servers.")
				name = strings.Trim(name, `"'`)
				current = &model.AgentTypeMCP{Name: name, Enabled: true}
			}
			continue
		}
		if current == nil {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "command":
			current.Command = trimTOMLString(v)
		case "args":
			current.Args = parseTOMLArray(v)
		case "url":
			current.URL = trimTOMLString(v)
			current.Transport = model.MCPHTTP
		case "enabled":
			current.Enabled = v == "true"
		}
	}
	flush()
	return out
}

func trimTOMLString(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.Trim(v, `"'`)
}

func parseTOMLArray(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	if i := strings.LastIndex(v, "]"); i >= 0 {
		v = v[:i]
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, trimTOMLString(part))
	}
	return out
}
