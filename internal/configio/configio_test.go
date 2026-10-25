package configio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/workspaces"
)

// fakeMounts serves both service interfaces without touching the kernel.
type fakeMounts struct{ root string }

func (f *fakeMounts) PrepareAgentTree(id string) error {
	for _, d := range []string{f.AgentDir(id), f.MemoryDir(id), f.WorkspaceTree(id), f.NotesDir(id)} {
		if err := mkdirAll(d); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeMounts) AgentDir(id string) string { return filepath.Join(f.root, "agents", id, "agent") }
func (f *fakeMounts) MemoryDir(id string) string {
	return filepath.Join(f.root, "agents", id, "memory")
}
func (f *fakeMounts) NotesDir(id string) string { return filepath.Join(f.root, "agents", id, "notes") }
func (f *fakeMounts) WorkspaceTree(id string) string {
	return filepath.Join(f.root, "agents", id, "workspace")
}
func (f *fakeMounts) List(string) ([]model.MountState, error)          { return nil, nil }
func (f *fakeMounts) Remove(string, string, bool) error                { return nil }
func (f *fakeMounts) TeardownAgentTree(string, bool) ([]string, error) { return nil, nil }
func (f *fakeMounts) ChownAgentTree(string, int, int) error            { return nil }
func (f *fakeMounts) ValidateSource(p string) (string, error)          { return p, nil }
func (f *fakeMounts) Add(id, source, target string, m model.AccessMode) (string, error) {
	return filepath.Join(f.WorkspaceTree(id), target), nil
}
func (f *fakeMounts) AddAllowedRoot(string) {}

func mkdirAll(p string) error {
	return os.MkdirAll(p, 0o755)
}

type env struct {
	svc *Service
	st  *persistence.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	st, err := persistence.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fm := &fakeMounts{root: root}
	mm, err := memory.New(root, func(id string) string {
		return filepath.Join(root, "memories", id)
	})
	if err != nil {
		t.Fatal(err)
	}
	as := &agents.Service{Store: st, Mounts: fm, Memory: mm, DefaultUID: 1000, DefaultGID: 1000, KnowledgeDir: "/var/lib/dockweave/knowledge"}
	ws := &workspaces.Service{Store: st, Mounts: fm}
	return &env{svc: &Service{Store: st, Agents: as, Workspaces: ws, DaemonConfig: map[string]string{"KNOWLEDGE_DIR": "/w"}}, st: st}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newEnv(t)

	// Build a representative setup.
	proj := t.TempDir()
	if _, err := src.svc.Workspaces.Register(ctx, "proj", proj, "test"); err != nil {
		t.Fatal(err)
	}
	a := &model.Agent{Name: "agent-a", Type: "codex", Image: "node:22-bookworm",
		Command: []string{"sh", "-c", "run"}, AgentRO: true, UID: 1000, GID: 1000, NetworkMode: "bridge"}
	if _, err := src.svc.Agents.Create(ctx, a, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.svc.Agents.SetNotesDir(ctx, "agent-a", "/home/krusty/notes-a", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.svc.Workspaces.Grant(ctx, "proj", "agent-a", model.ModeRW, true, time.Hour, "test"); err != nil {
		t.Fatal(err)
	}

	// Export -> JSON -> import into a fresh deployment.
	b, err := src.svc.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Agents) != 1 || len(b.Workspaces) != 1 || len(b.Grants) != 1 || len(b.Memories) != 1 {
		t.Fatalf("unexpected bundle size: %+v", b)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var b2 Bundle
	if err := json.Unmarshal(data, &b2); err != nil {
		t.Fatal(err)
	}

	dst := newEnv(t)
	res, err := dst.svc.Import(ctx, &b2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("import errors: %v", res.Errors)
	}

	// Everything must be recreated with the same configuration.
	if len(res.Created) != 4 { // memory + agent + workspace + grant
		t.Fatalf("created: %v", res.Created)
	}
	got, err := dst.st.GetAgent(ctx, "agent-a")
	if err != nil {
		t.Fatalf("agent not recreated: %v", err)
	}
	if got.Image != "node:22-bookworm" || got.UID != 1000 || got.NotesDir != "/home/krusty/notes-a" {
		t.Fatalf("agent config differs: %+v", got)
	}
	w, err := dst.st.GetWorkspace(ctx, "proj")
	if err != nil || w.HostPath != proj {
		t.Fatalf("workspace not recreated: %+v %v", w, err)
	}
	gs, err := dst.st.ListGrants(ctx, w.ID, got.ID, false)
	if err != nil || len(gs) != 1 || gs[0].Mode != model.ModeRW || !gs[0].Exclusive {
		t.Fatalf("grant not recreated: %+v %v", gs, err)
	}
	mem, err := dst.st.GetMemoryByAgent(ctx, got.ID)
	if err != nil {
		t.Fatalf("memory binding not recreated: %v", err)
	}
	if mem.Path == "" {
		t.Fatal("memory store path must be preserved")
	}
	// Idempotent: a second import skips everything.
	res2, err := dst.svc.Import(ctx, &b2)
	if err != nil || len(res2.Errors) != 0 {
		t.Fatalf("re-import: %v %v", res2, err)
	}
	if len(res2.Created) != 0 {
		t.Fatalf("re-import must create nothing: %v", res2.Created)
	}
}

func TestImportRejectsForeignFormat(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Import(context.Background(), &Bundle{Format: "something-else"}); err == nil {
		t.Fatal("foreign bundle format must be rejected")
	}
}
