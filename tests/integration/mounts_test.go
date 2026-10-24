//go:build integration
// +build integration

// Package integration runs privileged tests. Execute via:
//
//	make test-integration
//
// or directly inside a privileged container:
//
//	go test -tags integration ./tests/integration/...
//
// Tests skip unless running as root with mount capability.
package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
)

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root (privileged container)")
	}
}

func newManager(t *testing.T) (*mounts.Manager, string) {
	t.Helper()
	root := t.TempDir()
	allowed := filepath.Join(root, "projects")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := mounts.New(mounts.Config{Root: root, AllowedRoots: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	return m, allowed
}

// prepareTree prepares the tree and registers teardown so t.TempDir cleanup
// does not fail on the standing self-bind mount.
func prepareTree(t *testing.T, m *mounts.Manager, agentID string) {
	t.Helper()
	if err := m.PrepareAgentTree(agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = m.TeardownAgentTree(agentID, true)
	})
}

func TestPropagationProbe(t *testing.T) {
	requireRoot(t)
	m, _ := newManager(t)
	if err := m.PropagationProbe(); err != nil {
		t.Fatalf("propagation probe: %v", err)
	}
}

func TestPrepareAgentTreeShared(t *testing.T) {
	requireRoot(t)
	m, _ := newManager(t)
	if err := m.PrepareAgentTree("ag_test"); err != nil {
		t.Fatalf("prepare tree: %v", err)
	}
	t.Cleanup(func() { _, _ = m.TeardownAgentTree("ag_test", true) })
	if err := m.Check("ag_test"); err != nil {
		t.Fatalf("check after prepare: %v", err)
	}
	// Idempotent.
	if err := m.PrepareAgentTree("ag_test"); err != nil {
		t.Fatalf("second prepare: %v", err)
	}
}

func TestAddRemoveRW(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_rw")
	src := filepath.Join(allowed, "proj")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello"), 0o644)

	target, err := m.Add("ag_rw", src, "proj", model.ModeRW)
	if err != nil {
		t.Fatalf("add rw: %v", err)
	}
	// Verify mounted.
	list, err := m.List("ag_rw")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(list))
	}
	// Write should succeed.
	if err := os.WriteFile(filepath.Join(target, "out.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write to rw mount: %v", err)
	}
	// Remove.
	if err := m.Remove("ag_rw", "proj", false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ = m.List("ag_rw")
	if len(list) != 0 {
		t.Fatalf("expected 0 mounts after remove, got %d", len(list))
	}
}

func TestAddRemoveRO(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_ro")
	src := filepath.Join(allowed, "ro-proj")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644)

	target, err := m.Add("ag_ro", src, "ro-proj", model.ModeRO)
	if err != nil {
		t.Fatalf("add ro: %v", err)
	}
	// Write must fail.
	if err := os.WriteFile(filepath.Join(target, "hack.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("write to ro mount succeeded — RO not enforced")
	}
	// Read must work.
	b, err := os.ReadFile(filepath.Join(target, "f.txt"))
	if err != nil || string(b) != "data" {
		t.Fatalf("read from ro mount: %v %q", err, b)
	}
	if err := m.Remove("ag_ro", "ro-proj", false); err != nil {
		t.Fatalf("remove ro: %v", err)
	}
}

func TestDoubleAddIdempotent(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_dup")
	src := filepath.Join(allowed, "dup")
	os.MkdirAll(src, 0o755)

	if _, err := m.Add("ag_dup", src, "dup", model.ModeRW); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// Second add is a no-op.
	if _, err := m.Add("ag_dup", src, "dup", model.ModeRW); err != nil {
		t.Fatalf("second add should be idempotent: %v", err)
	}
	// Different mode is rejected.
	if _, err := m.Add("ag_dup", src, "dup", model.ModeRO); err == nil {
		t.Fatal("mode change should be rejected")
	}
	// Clean up.
	_ = m.Remove("ag_dup", "dup", true)
}

func TestAddSourceOutsideAllowlist(t *testing.T) {
	requireRoot(t)
	m, _ := newManager(t)
	prepareTree(t, m, "ag_sec")
	// /etc is not in the allowlist.
	if _, err := m.Add("ag_sec", "/etc", "evil", model.ModeRW); !errors.Is(err, mounts.ErrNotAllowed) {
		t.Fatalf("expected ErrNotAllowed, got %v", err)
	}
}

func TestAddSymlinkEscape(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_sym")
	evil := t.TempDir()
	link := filepath.Join(allowed, "evil-link")
	if err := os.Symlink(evil, link); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add("ag_sym", link, "evil", model.ModeRW); !errors.Is(err, mounts.ErrNotAllowed) {
		t.Fatalf("symlink escape should be rejected, got %v", err)
	}
}

func TestRemoveBusyReturnsEBUSY(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_busy")
	src := filepath.Join(allowed, "busy")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)

	target, err := m.Add("ag_busy", src, "busy", model.ModeRW)
	if err != nil {
		t.Fatal(err)
	}
	// Open a file on the mount to make it busy.
	f, err := os.Open(filepath.Join(target, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Non-force remove must return ErrBusy.
	if err := m.Remove("ag_busy", "busy", false); !errors.Is(err, mounts.ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	// Close the file, then remove succeeds.
	f.Close()
	time.Sleep(50 * time.Millisecond)
	if err := m.Remove("ag_busy", "busy", false); err != nil {
		t.Fatalf("remove after close: %v", err)
	}
}

func TestReconcileRestoresAndReportsOrphans(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_rec")
	src := filepath.Join(allowed, "proj")
	os.MkdirAll(src, 0o755)

	// Mount one project manually (simulates external mount → orphan).
	orphanSrc := filepath.Join(allowed, "orphan")
	os.MkdirAll(orphanSrc, 0o755)
	if _, err := m.Add("ag_rec", orphanSrc, "orphan", model.ModeRW); err != nil {
		t.Fatal(err)
	}

	desired := []model.MountState{
		{AgentID: "ag_rec", HostPath: src, TargetPath: filepath.Join(m.WorkspaceTree("ag_rec"), "proj"), Mode: model.ModeRW},
	}
	res, err := m.Reconcile("ag_rec", desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 {
		t.Fatalf("expected 1 restored, got %v", res.Restored)
	}
	if len(res.Orphaned) != 1 {
		t.Fatalf("expected 1 orphan, got %v", res.Orphaned)
	}
	// Clean up.
	_ = m.Remove("ag_rec", "orphan", true)
	_ = m.Remove("ag_rec", "proj", true)
}

func TestTargetNameValidation(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "../x"} {
		if err := mounts.SanitizeTargetName(bad); err == nil {
			t.Fatalf("invalid target %q accepted", bad)
		}
	}
	for _, ok := range []string{"proj", "a.b", "x-y_z"} {
		if err := mounts.SanitizeTargetName(ok); err != nil {
			t.Fatalf("valid target %q rejected: %v", ok, err)
		}
	}
}

var _ = context.Background

func TestAddSourceIdentityCheck(t *testing.T) {
	requireRoot(t)
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_id")
	srcA := filepath.Join(allowed, "proj-a")
	srcB := filepath.Join(allowed, "proj-b")
	os.MkdirAll(srcA, 0o755)
	os.MkdirAll(srcB, 0o755)

	if _, err := m.Add("ag_id", srcA, "proj", model.ModeRW); err != nil {
		t.Fatalf("add a: %v", err)
	}
	// Same source + same mode: idempotent.
	if _, err := m.Add("ag_id", srcA, "proj", model.ModeRW); err != nil {
		t.Fatalf("idempotent add must succeed: %v", err)
	}
	// Different source, same mode: conflict, must NOT be accepted.
	if _, err := m.Add("ag_id", srcB, "proj", model.ModeRW); err == nil {
		t.Fatal("add b over a must fail (source mismatch)")
	} else if !errors.Is(err, mounts.ErrConflict) {
		t.Fatalf("want ErrConflict, got: %v", err)
	}
	// Different source, different mode: still conflict (identity first).
	if _, err := m.Add("ag_id", srcB, "proj", model.ModeRO); err == nil {
		t.Fatal("add b(ro) over a(rw) must fail")
	}
	// Same source, different mode: mode error.
	if _, err := m.Add("ag_id", srcA, "proj", model.ModeRO); err == nil {
		t.Fatal("add a(ro) over a(rw) must fail (mode mismatch)")
	}

	// RO variant: staging must also refuse a source swap at the same target.
	if _, err := m.Add("ag_id", srcA, "proj-ro", model.ModeRO); err != nil {
		t.Fatalf("add ro a: %v", err)
	}
	if _, err := m.Add("ag_id", srcB, "proj-ro", model.ModeRO); err == nil {
		t.Fatal("add ro b over ro a must fail (source mismatch)")
	} else if !errors.Is(err, mounts.ErrConflict) {
		t.Fatalf("want ErrConflict, got: %v", err)
	}

	// Nothing leaked: exactly the two intended mounts.
	list, err := m.List("ag_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(list))
	}
}
