package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeFS simulates exec against an in-memory directory tree.
type fakeFS struct {
	dirs map[string][]string // dir -> names
}

func (f fakeFS) exec(ctx context.Context, cmd []string) (string, error) {
	// Support: test -d <dir>, ls -1 <dir>
	if len(cmd) >= 3 && cmd[0] == "test" && cmd[1] == "-d" {
		if _, ok := f.dirs[cmd[2]]; ok {
			return "", nil
		}
		return fmt.Sprintf("no dir %s", cmd[2]), fmt.Errorf("exit 1")
	}
	if len(cmd) >= 3 && cmd[0] == "ls" && cmd[1] == "-1" {
		if names, ok := f.dirs[cmd[2]]; ok {
			return strings.Join(names, "\n") + "\n", nil
		}
		return "", fmt.Errorf("no dir")
	}
	return "", fmt.Errorf("unsupported")
}

func TestCodexAdapter(t *testing.T) {
	fs := fakeFS{dirs: map[string][]string{
		"/memory/.codex":            {"sessions", "config.toml"},
		"/memory/.codex/sessions":   {"2026"},
		"/memory/.codex/sessions/2026": {"10-22-a.json", "10-23-b.json"},
	}}
	ctx := context.Background()
	reg := NewRegistry()
	a, ok := reg.DetectAll(ctx, fs.exec)
	if !ok || a.Name() != "codex" {
		t.Fatalf("expected codex detection, got %v %v", a, ok)
	}
	info, err := a.SessionInfo(ctx, fs.exec)
	if err != nil || !info.Detected {
		t.Fatalf("session info: %+v %v", info, err)
	}
	if len(info.Sessions) != 2 || info.Sessions[0] != "10-23-b" {
		t.Fatalf("sessions not newest-first: %v", info.Sessions)
	}
}

func TestClaudeAdapterActiveProject(t *testing.T) {
	fs := fakeFS{dirs: map[string][]string{
		"/memory/.claude":           {"projects"},
		"/memory/.claude/projects":  {"-workspace-proj"},
	}}
	ctx := context.Background()
	a := ClaudeAdapter{}
	if !a.Detect(ctx, fs.exec) {
		t.Fatal("claude not detected")
	}
	info, _ := a.SessionInfo(ctx, fs.exec)
	if !info.Detected || info.ActiveProject != "/workspace/proj" {
		t.Fatalf("active project not derived: %+v", info)
	}
}

func TestNoDetection(t *testing.T) {
	fs := fakeFS{dirs: map[string][]string{"/memory": {}}}
	_, ok := NewRegistry().DetectAll(context.Background(), fs.exec)
	if ok {
		t.Fatal("empty container must detect nothing")
	}
}

func TestOpencodeAndGemini(t *testing.T) {
	ctx := context.Background()
	fs := fakeFS{dirs: map[string][]string{
		"/memory/.local/share/opencode":            {"storage"},
		"/memory/.local/share/opencode/storage":     {"session"},
		"/memory/.local/share/opencode/storage/session": {"s2", "s1"},
		"/memory/.gemini":      {"tmp"},
		"/memory/.gemini/tmp":  {"g1"},
	}}
	a := OpencodeAdapter{}
	info, _ := a.SessionInfo(ctx, fs.exec)
	if !info.Detected || len(info.Sessions) != 2 || info.Sessions[0] != "s2" {
		t.Fatalf("opencode sessions wrong: %+v", info)
	}
	g := GeminiAdapter{}
	gi, _ := g.SessionInfo(ctx, fs.exec)
	if !gi.Detected || len(gi.Sessions) != 1 || gi.Sessions[0] != "g1" {
		t.Fatalf("gemini sessions wrong: %+v", gi)
	}
}
