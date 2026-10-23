package workspaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// fakeExecutor records mount operations without touching the kernel.
type fakeExecutor struct {
	added   []string
	removed []string
	roots   []string
	addErr  error
}

func (f *fakeExecutor) Add(agentID, source, targetName string, mode model.AccessMode) (string, error) {
	if f.addErr != nil {
		return "", f.addErr
	}
	f.added = append(f.added, agentID+":"+targetName+":"+string(mode))
	return "/var/lib/dockweave/agents/" + agentID + "/workspace/" + targetName, nil
}

func (f *fakeExecutor) Remove(agentID, targetName string, force bool) error {
	f.removed = append(f.removed, agentID+":"+targetName)
	return nil
}

func (f *fakeExecutor) WorkspaceTree(agentID string) string {
	return "/var/lib/dockweave/agents/" + agentID + "/workspace"
}

func (f *fakeExecutor) AddAllowedRoot(path string) { f.roots = append(f.roots, path) }

func setup(t *testing.T) (*Service, *fakeExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := persistence.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fx := &fakeExecutor{}
	return &Service{Store: st, Mounts: fx}, fx, dir
}

func mkAgent(t *testing.T, s *Service, name string) *model.Agent {
	a := &model.Agent{
		ID: model.NewID("ag"), Name: name, Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := s.Store.CreateAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRegisterCanonicalizes(t *testing.T) {
	svc, fx, dir := setup(t)
	ctx := context.Background()
	proj := filepath.Join(dir, "projects", "a")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink must be canonicalized.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(proj, link); err != nil {
		t.Fatal(err)
	}
	w, err := svc.Register(ctx, "proj-a", link, "test")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if w.HostPath != proj {
		t.Fatalf("host_path not canonicalized: %s != %s", w.HostPath, proj)
	}
	if len(fx.roots) != 1 || fx.roots[0] != proj {
		t.Fatalf("allowed root not registered: %v", fx.roots)
	}
	// Non-directory rejected.
	if _, err := svc.Register(ctx, "bad", dir, "test"); err == nil {
		// dir itself is a directory — use a file instead.
	}
	f := filepath.Join(dir, "f")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := svc.Register(ctx, "bad2", f, "test"); err == nil {
		t.Fatal("file registered as workspace")
	}
}

func TestGrantIdempotentAndConflicts(t *testing.T) {
	svc, fx, _ := setup(t)
	ctx := context.Background()
	dir := t.TempDir()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a1 := mkAgent(t, svc, "a1")
	a2 := mkAgent(t, svc, "a2")

	g1, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRW, true, 0, "test")
	if err != nil {
		t.Fatalf("grant rw: %v", err)
	}
	// Idempotent: same grant again returns existing.
	g1b, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRW, true, 0, "test")
	if err != nil || g1b.ID != g1.ID {
		t.Fatalf("expected idempotent grant, got %v %v", g1b, err)
	}
	// Exclusive rw blocks second rw grant.
	if _, err := svc.Grant(ctx, w.Name, a2.Name, model.ModeRW, false, 0, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected exclusive conflict, got %v", err)
	}
	// RO grant for second agent is fine even under exclusive.
	g2, err := svc.Grant(ctx, w.Name, a2.Name, model.ModeRO, false, 0, "test")
	if err != nil {
		t.Fatalf("ro grant under exclusive: %v", err)
	}
	// Different mode for same agent conflicts (must revoke first).
	if _, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRO, false, 0, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected mode-change conflict, got %v", err)
	}
	if len(fx.added) != 2 {
		t.Fatalf("expected 2 mount adds, got %v", fx.added)
	}
	// Revoke and remount.
	if err := svc.Revoke(ctx, g1.ID, "test", false); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.Revoke(ctx, g2.ID, "test", false); err != nil {
		t.Fatalf("revoke g2: %v", err)
	}
	if len(fx.removed) != 2 {
		t.Fatalf("expected unmounts on revoke, got %v", fx.removed)
	}
	if _, err := svc.Grant(ctx, w.Name, a2.Name, model.ModeRW, false, 0, "test"); err != nil {
		t.Fatalf("rw grant after revoke: %v", err)
	}
}

func TestGrantTTL(t *testing.T) {
	svc, _, dir := setup(t)
	ctx := context.Background()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := mkAgent(t, svc, "a1")
	g, err := svc.Grant(ctx, w.Name, a.Name, model.ModeRO, false, time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	if g.ExpiresAt == nil || !g.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiry not set: %+v", g.ExpiresAt)
	}
}

func TestUnregisterRequiresRevoke(t *testing.T) {
	svc, _, dir := setup(t)
	ctx := context.Background()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := mkAgent(t, svc, "a1")
	g, err := svc.Grant(ctx, w.Name, a.Name, model.ModeRO, false, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Unregister(ctx, w.Name, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unregister with active grant must fail, got %v", err)
	}
	if err := svc.Revoke(ctx, g.ID, "test", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Unregister(ctx, w.Name, "test"); err != nil {
		t.Fatalf("unregister after revoke: %v", err)
	}
}
