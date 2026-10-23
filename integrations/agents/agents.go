// Package agents (integrations/agents) provides optional adapters that
// extract session metadata from well-known coding agents (Codex CLI,
// OpenCode, Claude Code, Gemini CLI). Adapters are pure enrichment: the
// core system works fully without them, and every adapter reports
// "not detected" instead of guessing.
package agents

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
)

// ExecFunc runs a command inside the agent container and returns combined
// output. Adapters never assume a shell; commands are argv only.
type ExecFunc func(ctx context.Context, cmd []string) (string, error)

// SessionInfo is optional metadata reported by an adapter.
type SessionInfo struct {
	Adapter      string            `json:"adapter"`
	Detected     bool              `json:"detected"`
	ActiveProject string           `json:"active_project,omitempty"` // reported by the agent, if any
	Sessions     []string          `json:"sessions,omitempty"`       // session/checkpoint identifiers
	Details      map[string]string `json:"details,omitempty"`
}

// Adapter detects one agent flavor and extracts session metadata.
type Adapter interface {
	// Name returns the adapter identifier (codex, opencode, claude, gemini).
	Name() string
	// Detect reports whether the agent's marker files exist in the container.
	Detect(ctx context.Context, exec ExecFunc) bool
	// SessionInfo extracts optional metadata; returns Detected=false when
	// the agent is not present.
	SessionInfo(ctx context.Context, exec ExecFunc) (*SessionInfo, error)
}

// Registry holds all known adapters.
type Registry struct {
	adapters []Adapter
}

// NewRegistry returns a registry with all built-in adapters.
func NewRegistry() *Registry {
	return &Registry{adapters: []Adapter{
		&CodexAdapter{},
		&OpencodeAdapter{},
		&ClaudeAdapter{},
		&GeminiAdapter{},
	}}
}

// Adapters returns the registered adapters.
func (r *Registry) Adapters() []Adapter { return r.adapters }

// DetectAll probes all adapters, returning the first match (an agent
// container typically hosts exactly one coding agent).
func (r *Registry) DetectAll(ctx context.Context, exec ExecFunc) (Adapter, bool) {
	for _, a := range r.adapters {
		if a.Detect(ctx, exec) {
			return a, true
		}
	}
	return nil, false
}

// markerDetect runs `test -d <dir>` via exec to detect a marker directory.
func markerDetect(ctx context.Context, exec ExecFunc, dir string) bool {
	out, err := exec(ctx, []string{"test", "-d", dir})
	return err == nil && strings.TrimSpace(out) == ""
}

// listDir lists a directory via exec, one name per line; empty on error.
func listDir(ctx context.Context, exec ExecFunc, dir string) []string {
	out, err := exec(ctx, []string{"ls", "-1", dir})
	if err != nil {
		return nil
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			names = append(names, l)
		}
	}
	return names
}

// firstExisting returns the first marker dir that exists.
func firstExisting(ctx context.Context, exec ExecFunc, dirs ...string) string {
	for _, d := range dirs {
		if markerDetect(ctx, exec, d) {
			return d
		}
	}
	return ""
}

// newestNames sorts names descending and returns at most n.
func newestNames(names []string, n int) []string {
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > n {
		names = names[:n]
	}
	return names
}

// ---- Codex CLI ----

// CodexAdapter detects OpenAI Codex CLI (~/.codex sessions).
type CodexAdapter struct{}

// Name implements Adapter.
func (CodexAdapter) Name() string { return "codex" }

// Detect implements Adapter.
func (CodexAdapter) Detect(ctx context.Context, exec ExecFunc) bool {
	return firstExisting(ctx, exec, "/memory/.codex", "/root/.codex") != ""
}

// SessionInfo implements Adapter: lists recent Codex session files.
func (CodexAdapter) SessionInfo(ctx context.Context, exec ExecFunc) (*SessionInfo, error) {
	base := firstExisting(ctx, exec, "/memory/.codex", "/root/.codex")
	if base == "" {
		return &SessionInfo{Adapter: "codex", Detected: false}, nil
	}
	sessions := listDir(ctx, exec, filepath.Join(base, "sessions"))
	// Session files are nested by date; flatten one level if needed.
	if len(sessions) > 0 && !strings.Contains(sessions[0], ".json") {
		var flat []string
		for _, d := range sessions {
			flat = append(flat, listDir(ctx, exec, filepath.Join(base, "sessions", d))...)
		}
		sessions = flat
	}
	var trimmed []string
	for _, s := range newestNames(sessions, 10) {
		trimmed = append(trimmed, strings.TrimSuffix(s, ".json"))
	}
	return &SessionInfo{
		Adapter:  "codex",
		Detected: true,
		Sessions: trimmed,
	}, nil
}

// ---- OpenCode ----

// OpencodeAdapter detects OpenCode (~/.local/share/opencode).
type OpencodeAdapter struct{}

// Name implements Adapter.
func (OpencodeAdapter) Name() string { return "opencode" }

// Detect implements Adapter.
func (OpencodeAdapter) Detect(ctx context.Context, exec ExecFunc) bool {
	return firstExisting(ctx, exec, "/memory/.local/share/opencode", "/root/.local/share/opencode") != ""
}

// SessionInfo implements Adapter.
func (OpencodeAdapter) SessionInfo(ctx context.Context, exec ExecFunc) (*SessionInfo, error) {
	base := firstExisting(ctx, exec, "/memory/.local/share/opencode", "/root/.local/share/opencode")
	if base == "" {
		return &SessionInfo{Adapter: "opencode", Detected: false}, nil
	}
	sessions := listDir(ctx, exec, filepath.Join(base, "storage", "session"))
	return &SessionInfo{
		Adapter:  "opencode",
		Detected: true,
		Sessions: newestNames(sessions, 10),
	}, nil
}

// ---- Claude Code ----

// ClaudeAdapter detects Claude Code (~/.claude).
type ClaudeAdapter struct{}

// Name implements Adapter.
func (ClaudeAdapter) Name() string { return "claude" }

// Detect implements Adapter.
func (ClaudeAdapter) Detect(ctx context.Context, exec ExecFunc) bool {
	return firstExisting(ctx, exec, "/memory/.claude", "/root/.claude") != ""
}

// SessionInfo implements Adapter: lists Claude Code project directories.
func (ClaudeAdapter) SessionInfo(ctx context.Context, exec ExecFunc) (*SessionInfo, error) {
	base := firstExisting(ctx, exec, "/memory/.claude", "/root/.claude")
	if base == "" {
		return &SessionInfo{Adapter: "claude", Detected: false}, nil
	}
	projects := listDir(ctx, exec, filepath.Join(base, "projects"))
	info := &SessionInfo{Adapter: "claude", Detected: true}
	for _, p := range projects {
		// Project dirs encode the path: -home-user-project
		info.Sessions = append(info.Sessions, p)
		if info.ActiveProject == "" {
			// Newest project dir is a best-effort active-project hint.
			// Encoded path "-workspace-proj" -> "/workspace/proj"
			info.ActiveProject = "/" + strings.ReplaceAll(strings.TrimPrefix(p, "-"), "-", "/")
		}
	}
	info.Sessions = newestNames(info.Sessions, 10)
	return info, nil
}

// ---- Gemini CLI ----

// GeminiAdapter detects Gemini CLI (~/.gemini).
type GeminiAdapter struct{}

// Name implements Adapter.
func (GeminiAdapter) Name() string { return "gemini" }

// Detect implements Adapter.
func (GeminiAdapter) Detect(ctx context.Context, exec ExecFunc) bool {
	return firstExisting(ctx, exec, "/memory/.gemini", "/root/.gemini") != ""
}

// SessionInfo implements Adapter.
func (GeminiAdapter) SessionInfo(ctx context.Context, exec ExecFunc) (*SessionInfo, error) {
	base := firstExisting(ctx, exec, "/memory/.gemini", "/root/.gemini")
	if base == "" {
		return &SessionInfo{Adapter: "gemini", Detected: false}, nil
	}
	tmp := listDir(ctx, exec, filepath.Join(base, "tmp"))
	return &SessionInfo{
		Adapter:  "gemini",
		Detected: true,
		Sessions: newestNames(tmp, 10),
	}, nil
}
