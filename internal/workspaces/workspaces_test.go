package workspaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

// fakeExecutor records mount operations without touching the kernel.
type fakeExecutor struct {
	added     []string
	removed   []string
	roots     []string
	addErr    error
	removeErr error
}

func (f *fakeExecutor) Add(agentID, source, targetName string, mode model.AccessMode) (string, error) {
	if f.addErr != nil {
		return "", f.addErr
	}
	f.added = append(f.added, agentID+":"+targetName+":"+string(mode))
	return "/var/lib/dockweave/agents/" + agentID + "/workspace/" + targetName, nil
}

func (f *fakeExecutor) Remove(agentID, targetName string, force bool) error {
	if f.removeErr != nil {
		return f.removeErr
	}
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

func TestGrantMountFailureLeavesNoActivePermission(t *testing.T) {
	svc, fx, dir := setup(t)
	ctx := context.Background()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := mkAgent(t, svc, "a1")

	fx.addErr = errors.New("mount exploded")
	g, err := svc.Grant(ctx, w.Name, a.Name, model.ModeRO, false, 0, "test")
	if err == nil {
		t.Fatal("grant with failing mount must return an error")
	}
	if g != nil {
		t.Fatalf("no grant record expected in return, got %+v", g)
	}
	// The record exists but must not be an active permission.
	grants, err := svc.Store.ListGrants(ctx, w.ID, a.ID, true)
	if err != nil || len(grants) != 1 {
		t.Fatalf("expected 1 recorded grant, got %d (%v)", len(grants), err)
	}
	got := grants[0]
	if got.Status != model.GrantMountFailed {
		t.Fatalf("status = %q, want mount_failed", got.Status)
	}
	if got.Active(time.Now()) {
		t.Fatal("mount_failed grant must not be active")
	}

	// Retry must not be blocked by the failed record.
	fx.addErr = nil
	g2, err := svc.Grant(ctx, w.Name, a.Name, model.ModeRO, false, 0, "test")
	if err != nil {
		t.Fatalf("retry grant: %v", err)
	}
	if g2.Status != model.GrantActive || !g2.Active(time.Now()) {
		t.Fatalf("retry grant must be active, got %q", g2.Status)
	}
}

func TestRevokeUnmountFailureKeepsClaim(t *testing.T) {
	svc, fx, dir := setup(t)
	ctx := context.Background()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a1 := mkAgent(t, svc, "a1")
	a2 := mkAgent(t, svc, "a2")
	g, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRW, false, 0, "test")
	if err != nil {
		t.Fatal(err)
	}

	fx.removeErr = mounts.ErrBusy
	err = svc.Revoke(ctx, g.ID, "test", false)
	if err == nil {
		t.Fatal("revoke with busy unmount must fail")
	}
	// The grant must NOT be revoked while the mount is still live.
	got, err := svc.Store.GetGrant(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt != nil {
		t.Fatal("grant must not be revoked when unmount failed")
	}
	if got.Status != model.GrantUnmountFailed {
		t.Fatalf("status = %q, want unmount_failed", got.Status)
	}
	if !got.Pending(time.Now()) {
		t.Fatal("unmount_failed grant must still claim the pair")
	}
	// A new grant for the same agent must conflict (claim held).
	if _, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRW, false, 0, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict while unmount_failed, got %v", err)
	}
	// Another agent is unaffected (per-pair claim).
	if _, err := svc.Grant(ctx, w.Name, a2.Name, model.ModeRO, false, 0, "test"); err != nil {
		t.Fatalf("other agent grant while unmount_failed: %v", err)
	}

	// Retry succeeds and finalizes the revoke.
	fx.removeErr = nil
	if err := svc.Revoke(ctx, g.ID, "test", false); err != nil {
		t.Fatalf("retry revoke: %v", err)
	}
	got, _ = svc.Store.GetGrant(ctx, g.ID)
	if got.RevokedAt == nil || got.Status != model.GrantRevoked {
		t.Fatalf("after retry: status=%q revoked=%v", got.Status, got.RevokedAt)
	}
	if _, err := svc.Grant(ctx, w.Name, a1.Name, model.ModeRW, false, 0, "test"); err != nil {
		t.Fatalf("grant after completed revoke: %v", err)
	}
}

func TestReconcileGrantsConvergesInterruptedLifecycles(t *testing.T) {
	svc, fx, dir := setup(t)
	ctx := context.Background()
	w, err := svc.Register(ctx, "proj", dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := mkAgent(t, svc, "a1")
	now := time.Now()

	// Simulate a crash during creation: 'mounting' row, no mount applied.
	gMounting := &model.Grant{
		ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID,
		Mode: model.ModeRO, Status: model.GrantMounting, CreatedAt: now, CreatedBy: "test",
	}
	if err := svc.Store.CreateGrant(ctx, gMounting); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash during revoke: 'revoking' row, mount still live.
	gRevoking := &model.Grant{
		ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID,
		Mode: model.ModeRW, Status: model.GrantRevoking, CreatedAt: now, CreatedBy: "test",
	}
	// The unique index allows one pending pair claim — create via direct
	// insert semantics: revoke the mounting one first would change states;
	// instead use a second agent for the revoking scenario.
	a2 := mkAgent(t, svc, "a2")
	gRevoking.AgentID = a2.ID
	if err := svc.Store.CreateGrant(ctx, gRevoking); err != nil {
		t.Fatal(err)
	}

	svc.ReconcileGrants(ctx)

	g1, _ := svc.Store.GetGrant(ctx, gMounting.ID)
	if g1.Status != model.GrantMountFailed {
		t.Fatalf("interrupted mounting: status=%q, want mount_failed", g1.Status)
	}
	g2, _ := svc.Store.GetGrant(ctx, gRevoking.ID)
	if g2.RevokedAt == nil || g2.Status != model.GrantRevoked {
		t.Fatalf("interrupted revoking: status=%q revoked=%v, want revoked", g2.Status, g2.RevokedAt)
	}
	// The revoking mount was unmounted exactly once, the mounting one cleaned.
	foundRevoking := false
	for _, r := range fx.removed {
		if r == a2.ID+":proj" {
			foundRevoking = true
		}
	}
	if !foundRevoking {
		t.Fatalf("expected unmount of revoking grant's target, got %v", fx.removed)
	}
}
