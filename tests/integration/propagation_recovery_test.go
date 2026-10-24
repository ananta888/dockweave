//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/workspaces"
)

// logKernel documents the kernel under test (review-007: the RO-staging
// semantics were validated under WSL2; CI runs on native kernels).
func logKernel(t *testing.T) {
	t.Helper()
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		t.Logf("kernel: %s", bytes.TrimSpace(b))
	}
}

// TestROPropagationIntoSlaveNamespace verifies the Phase-1 RO contract on
// this kernel (review-007): a staged read-only bind, moved into the shared
// workspace tree, must appear *read-only* inside a slave mount namespace
// (the agent container's rslave semantics) — including nested submounts of
// a recursive bind — and writes must fail there.
func TestROPropagationIntoSlaveNamespace(t *testing.T) {
	requireRoot(t)
	logKernel(t)
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare(1) not available")
	}
	m, allowed := newManager(t)
	prepareTree(t, m, "ag_prop")
	src := filepath.Join(allowed, "proj")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)

	// Nested rw submount inside the source: the recursive bind must bring
	// it along read-only (staging remount is recursive).
	nested := filepath.Join(allowed, "nested")
	os.MkdirAll(nested, 0o755)
	if err := unix.Mount(nested, filepath.Join(src, "sub"), "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("nested bind: %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(filepath.Join(src, "sub"), 0) })

	dir := t.TempDir()
	inNS := filepath.Join(dir, "in-ns")
	ready := filepath.Join(dir, "ready")
	target := filepath.Join(m.WorkspaceTree("ag_prop"), "proj")

	// Slave namespace child: waits for the mount, then verifies ro + no write.
	script := fmt.Sprintf(`
		: > %q
		while [ ! -f %q ]; do sleep 0.05; done
		if ! grep -q " %s " /proc/self/mountinfo; then echo "mount did not propagate"; exit 2; fi
		if ! grep " %s " /proc/self/mountinfo | head -1 | grep -q " ro,"; then echo "target not ro in slave ns"; exit 3; fi
		if ! grep " %s/sub " /proc/self/mountinfo | head -1 | grep -q " ro,"; then echo "nested submount not ro in slave ns"; exit 4; fi
		if touch %s/evil 2>/dev/null; then echo "write succeeded through ro mount in slave ns"; exit 5; fi
		exit 0
	`, inNS, ready, target, target, target, target)
	cmd := exec.Command("unshare", "--propagation=slave", "-m", "sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start slave ns child: %v", err)
	}
	// Wait until the child is inside its slave namespace.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(inNS); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("child never entered its mount namespace")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Staged RO bind into the shared tree: must propagate as ro.
	if _, err := m.Add("ag_prop", src, "proj", model.ModeRO); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("add ro: %v", err)
	}
	if err := os.WriteFile(ready, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := out.Read(buf)
	werr := cmd.Wait()
	if werr != nil {
		t.Fatalf("slave ns verification failed: %v (output: %q stderr: %q)", werr, string(buf[:n]), stderr.String())
	}

	// The parent namespace must see it read-only as well.
	f, err := os.OpenFile(filepath.Join(target, "evil2"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
		t.Fatal("write through staged ro mount must fail in the parent namespace")
	}
	if rw, err := anyRW(t, m, "ag_prop"); err != nil || rw {
		t.Fatalf("no rw mounts expected under the tree: rw=%v err=%v", rw, err)
	}
}

// anyRW reports whether any mount beneath the agent tree is read-write.
func anyRW(t *testing.T, m *mounts.Manager, agentID string) (bool, error) {
	t.Helper()
	list, err := m.List(agentID)
	if err != nil {
		return false, err
	}
	for _, ms := range list {
		if ms.Mode == model.ModeRW {
			return true, nil
		}
	}
	return false, nil
}

// TestCrashRestartConverges simulates a daemon crash between operations and
// the restart recovery (review-007): the mount registry and the grants must
// converge — live mounts stay usable, interrupted grant creations are
// finalized, and a clean revoke still works afterwards.
func TestCrashRestartConverges(t *testing.T) {
	requireRoot(t)
	logKernel(t)
	root := t.TempDir()
	allowed := filepath.Join(root, "projects")
	proj := filepath.Join(allowed, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	dbPath := filepath.Join(root, "t.db")

	ctx := context.Background()
	m1, err := mounts.New(mounts.Config{Root: root, AllowedRoots: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	st1, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ws1 := &workspaces.Service{Store: st1, Mounts: m1}

	a := &model.Agent{
		ID: "ag_crash", Name: "crash-test", Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st1.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := m1.PrepareAgentTree(a.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m1.TeardownAgentTree(a.ID, true) })

	w, err := ws1.Register(ctx, "proj", proj, "test")
	if err != nil {
		t.Fatal(err)
	}
	g, err := ws1.Grant(ctx, w.Name, a.ID, model.ModeRW, false, 0, "test")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	// --- simulated crash: first generation of handles dies ---
	st1.Close()

	// --- restart: fresh manager + store on the same state ---
	m2, err := mounts.New(mounts.Config{Root: root, AllowedRoots: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	st2, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	ws2 := &workspaces.Service{Store: st2, Mounts: m2}

	// Recovery sequence as performed by the daemon at startup.
	if err := m2.PrepareAgentTree(a.ID); err != nil {
		t.Fatalf("re-prepare tree: %v", err)
	}
	desired, err := st2.ListMounts(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var want []model.MountState
	for _, d := range desired {
		if d.Status != model.MountGone {
			want = append(want, *d)
		}
	}
	if _, err := m2.Reconcile(a.ID, want); err != nil {
		t.Fatalf("reconcile mounts: %v", err)
	}
	list, err := m2.List(a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected the grant mount to survive recovery: %v %v", list, err)
	}
	gs, err := st2.ListGrants(ctx, w.ID, "", false)
	if err != nil || len(gs) != 1 || !gs[0].Active(time.Now()) {
		t.Fatalf("expected one active grant after recovery: %v %v", gs, err)
	}

	// Half-finished grant creation (crash between record and mount):
	// recovery must finalize it — never leave it 'mounting'.
	a2 := &model.Agent{
		ID: "ag_crash2", Name: "crash-test2", Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st2.CreateAgent(ctx, a2); err != nil {
		t.Fatal(err)
	}
	if err := m2.PrepareAgentTree(a2.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m2.TeardownAgentTree(a2.ID, true) })
	gm := &model.Grant{
		ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a2.ID,
		Mode: model.ModeRO, Status: model.GrantMounting, CreatedAt: time.Now(), CreatedBy: "crash",
	}
	if err := st2.CreateGrant(ctx, gm); err != nil {
		t.Fatal(err)
	}
	ws2.ReconcileGrants(ctx)
	got, err := st2.GetGrant(ctx, gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.GrantMountFailed || got.Active(time.Now()) {
		t.Fatalf("interrupted creation must end as mount_failed, got %q", got.Status)
	}

	// Clean shutdown after recovery: revoke still converges.
	if err := ws2.Revoke(ctx, g.ID, "test", false); err != nil {
		t.Fatalf("revoke after recovery: %v", err)
	}
	list, _ = m2.List(a.ID)
	if len(list) != 0 {
		t.Fatalf("mount must be gone after revoke: %v", list)
	}
	gs, _ = st2.ListGrants(ctx, w.ID, "", true)
	for _, x := range gs {
		if x.ID == g.ID && x.RevokedAt == nil {
			t.Fatal("grant must be revoked")
		}
	}
}
