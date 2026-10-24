package agents

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// fakeMounts implements MountOps with error injection for prune tests.
type fakeMounts struct {
	root           string
	list           []model.MountState
	graceful       map[string]error
	forcedErr      error
	teardownErr    error
	teardownDetach string
	chown          [][2]int
	calls          []string
}

func (f *fakeMounts) PrepareAgentTree(id string) error {
	for _, d := range []string{f.AgentDir(id), f.MemoryDir(id), f.WorkspaceTree(id)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeMounts) AgentDir(id string) string { return filepath.Join(f.root, "agents", id, "agent") }
func (f *fakeMounts) MemoryDir(id string) string {
	return filepath.Join(f.root, "agents", id, "memory")
}
func (f *fakeMounts) WorkspaceTree(id string) string {
	return filepath.Join(f.root, "agents", id, "workspace")
}
func (f *fakeMounts) List(string) ([]model.MountState, error) { return f.list, nil }

func (f *fakeMounts) TeardownAgentTree(_ string, force bool) ([]string, error) {
	if f.teardownErr != nil {
		return nil, f.teardownErr
	}
	if force && f.teardownDetach != "" {
		return []string{f.teardownDetach}, nil
	}
	return nil, nil
}

func (f *fakeMounts) ChownAgentTree(_ string, uid, gid int) error {
	f.chown = append(f.chown, [2]int{uid, gid})
	return nil
}

func (f *fakeMounts) Remove(_ string, targetName string, force bool) error {
	tag := "graceful"
	if force {
		tag = "force"
	}
	f.calls = append(f.calls, targetName+":"+tag)
	if force {
		return f.forcedErr
	}
	return f.graceful[targetName]
}

// fakeDocker implements DockerOps with minimal bookkeeping.
type fakeDocker struct {
	removed []string
	rmErr   error
}

func (f *fakeDocker) EnsureImage(context.Context, string, io.Writer) error { return nil }
func (f *fakeDocker) Create(context.Context, dockerclient.ContainerSpec) (string, error) {
	return "cid", nil
}
func (f *fakeDocker) Start(context.Context, string) error     { return nil }
func (f *fakeDocker) Stop(context.Context, string, int) error { return nil }
func (f *fakeDocker) Remove(_ context.Context, id string, _ bool) error {
	if f.rmErr != nil {
		return f.rmErr
	}
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeDocker) FindByAgent(context.Context, string) (string, error) { return "cid", nil }
func (f *fakeDocker) State(context.Context, string) (string, error)       { return "exited", nil }
func (f *fakeDocker) ExecOutput(context.Context, string, []string) (string, error) {
	return "", nil
}

func setup(t *testing.T) (*Service, *fakeMounts, *fakeDocker, *persistence.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := persistence.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fm := &fakeMounts{root: dir, graceful: map[string]error{}}
	fd := &fakeDocker{}
	svc := &Service{Store: st, Mounts: fm, Docker: fd}
	return svc, fm, fd, st
}

func mkAgent(t *testing.T, svc *Service, name string) *model.Agent {
	t.Helper()
	a := &model.Agent{
		ID: model.NewID("ag"), Name: name, Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := svc.Store.CreateAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func hasEvent(t *testing.T, st *persistence.Store, typ model.EventType) bool {
	t.Helper()
	evs, err := st.ListEvents(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func TestRemovePruneAggregatesErrorsAndKeepsRecord(t *testing.T) {
	svc, fm, _, st := setup(t)
	ctx := context.Background()
	a := mkAgent(t, svc, "a1")
	fm.list = []model.MountState{
		{TargetPath: fm.WorkspaceTree(a.ID) + "/proj-a"},
		{TargetPath: fm.WorkspaceTree(a.ID) + "/proj-b"},
	}
	fm.graceful["proj-a"] = errors.New("busy")
	fm.graceful["proj-b"] = errors.New("busy")

	err := svc.Remove(ctx, a.ID, "test", true, false)
	if err == nil {
		t.Fatal("prune with failing unmounts must return an aggregated error")
	}
	for _, name := range []string{"proj-a", "proj-b"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error must mention %q, got: %v", name, err)
		}
	}
	// No silent MNT_DETACH without force.
	for _, c := range fm.calls {
		if strings.HasSuffix(c, ":force") {
			t.Fatalf("no forced detach allowed without force flag: %v", fm.calls)
		}
	}
	if hasEvent(t, st, model.EventUnmountForced) {
		t.Fatal("no forced-unmount audit event expected")
	}
	// The agent record must still exist: the prune stays retryable.
	if _, err := st.GetAgent(ctx, a.ID); err != nil {
		t.Fatalf("agent record must be kept on failed prune: %v", err)
	}
}

func TestRemovePruneForceDetachesAndAudits(t *testing.T) {
	svc, fm, _, st := setup(t)
	ctx := context.Background()
	a := mkAgent(t, svc, "a1")
	fm.list = []model.MountState{{TargetPath: fm.WorkspaceTree(a.ID) + "/proj"}}
	fm.graceful["proj"] = errors.New("busy")

	if err := svc.Remove(ctx, a.ID, "test", true, true); err != nil {
		t.Fatalf("force prune: %v", err)
	}
	// Graceful first, then exactly one audited force detach.
	if len(fm.calls) != 2 || fm.calls[0] != "proj:graceful" || fm.calls[1] != "proj:force" {
		t.Fatalf("want graceful-then-force, got %v", fm.calls)
	}
	if !hasEvent(t, st, model.EventUnmountForced) {
		t.Fatal("forced detach must be audited")
	}
	if _, err := st.GetAgent(ctx, a.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent must be deleted after clean force prune, got %v", err)
	}
}

func TestRemovePruneSuccessDeletesRecord(t *testing.T) {
	svc, fm, fd, st := setup(t)
	ctx := context.Background()
	a := mkAgent(t, svc, "a1")
	fm.list = []model.MountState{{TargetPath: fm.WorkspaceTree(a.ID) + "/proj"}}

	if err := svc.Remove(ctx, a.ID, "test", true, false); err != nil {
		t.Fatalf("clean prune: %v", err)
	}
	if !hasEvent(t, st, model.EventAgentRemoved) {
		t.Fatal("removal must be audited")
	}
	if _, err := st.GetAgent(ctx, a.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent must be gone, got %v", err)
	}
	if len(fd.removed) != 1 {
		t.Fatalf("container must be removed, got %v", fd.removed)
	}
}

func TestRemoveWithoutPruneKeepsHostData(t *testing.T) {
	svc, fm, _, st := setup(t)
	ctx := context.Background()
	a := mkAgent(t, svc, "a1")
	fm.list = []model.MountState{{TargetPath: fm.WorkspaceTree(a.ID) + "/proj"}}

	if err := svc.Remove(ctx, a.ID, "test", false, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fm.calls) != 0 {
		t.Fatalf("mounts must be untouched without prune, got %v", fm.calls)
	}
	if _, err := st.GetAgent(ctx, a.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent must be gone, got %v", err)
	}
}

func TestRemoveAbortsWithoutPruneWhenContainerRemoveFails(t *testing.T) {
	svc, _, fd, st := setup(t)
	ctx := context.Background()
	a := mkAgent(t, svc, "a1")
	fd.rmErr = errors.New("docker down")

	if err := svc.Remove(ctx, a.ID, "test", false, false); err == nil {
		t.Fatal("container removal failure must fail the remove")
	}
	// Record kept: no dangling container without a record.
	if _, err := st.GetAgent(ctx, a.ID); err != nil {
		t.Fatalf("agent record must be kept: %v", err)
	}
}

func TestCreateDefaultsToNonRootIdentity(t *testing.T) {
	svc, fm, _, st := setup(t)
	svc.DefaultUID, svc.DefaultGID = 1000, 1000
	ctx := context.Background()

	a := &model.Agent{Name: "n1", Type: "custom", Image: "img"}
	got, err := svc.Create(ctx, a, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != 1000 || got.GID != 1000 {
		t.Fatalf("identity = %d:%d, want 1000:1000 (non-root default)", got.UID, got.GID)
	}
	if !hasEvent(t, st, model.EventAgentIdentity) {
		t.Fatal("identity defaulting must be audited")
	}
	if len(fm.chown) != 1 || fm.chown[0] != [2]int{1000, 1000} {
		t.Fatalf("host tree must be handed to the container identity, got %v", fm.chown)
	}
}

func TestCreateHonorsExplicitNonRootUID(t *testing.T) {
	svc, _, _, st := setup(t)
	ctx := context.Background()

	a := &model.Agent{Name: "n2", Type: "custom", Image: "img", UID: 42, GID: 42}
	got, err := svc.Create(ctx, a, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != 42 || got.GID != 42 {
		t.Fatalf("identity = %d:%d, want 42:42", got.UID, got.GID)
	}
	if hasEvent(t, st, model.EventAgentIdentity) {
		t.Fatal("explicit non-root identity must not be overridden (no identity event)")
	}
}

func TestCreateRootOnlyWhenAllowedAndAudited(t *testing.T) {
	svc, _, _, st := setup(t)
	ctx := context.Background()

	// Root refused (default): identity is defaulted.
	a := &model.Agent{Name: "n3", Type: "custom", Image: "img"}
	got, err := svc.Create(ctx, a, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.UID == 0 {
		t.Fatal("root container must not be created without AllowRootContainer")
	}

	// Root allowed explicitly: honored and audited.
	svc.AllowRootContainer = true
	b := &model.Agent{Name: "n4", Type: "custom", Image: "img"}
	got, err = svc.Create(ctx, b, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != 0 || got.GID != 0 {
		t.Fatalf("explicit root must be honored when allowed, got %d:%d", got.UID, got.GID)
	}
	evs, err := st.ListEvents(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	var rootAudit bool
	for _, e := range evs {
		if e.Type == model.EventAgentIdentity && strings.Contains(e.Message, "root") {
			rootAudit = true
		}
	}
	if !rootAudit {
		t.Fatal("root container must be audited")
	}
}

// ---- memory identity lifecycle (review-009) ----

func setupWithMemory(t *testing.T) (*Service, *fakeMounts, *fakeDocker, *persistence.Store, *memory.Manager) {
	t.Helper()
	svc, fm, fd, st := setup(t)
	mm, err := memory.New(fm.root, func(memID string) string {
		return filepath.Join(fm.root, "memories", memID)
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Memory = mm
	// Use the effective identity so chown-to-self works unprivileged
	// (CI runners do not run as uid 1000).
	svc.DefaultUID, svc.DefaultGID = os.Getuid(), os.Getgid()
	return svc, fm, fd, st, mm
}

func TestCreateBindsDedicatedMemory(t *testing.T) {
	svc, _, _, st, mm := setupWithMemory(t)
	ctx := context.Background()
	a := &model.Agent{Name: "m1", Type: "custom", Image: "img"}
	if _, err := svc.Create(ctx, a, "test"); err != nil {
		t.Fatal(err)
	}
	mem, err := st.GetMemoryByAgent(ctx, a.ID)
	if err != nil {
		t.Fatalf("agent must own a memory identity: %v", err)
	}
	if _, err := os.Stat(mm.Path(mem.ID)); err != nil {
		t.Fatalf("memory store dir missing: %v", err)
	}
}

func TestMemoryAttachDetachAndSingleWriter(t *testing.T) {
	svc, _, _, st, mm := setupWithMemory(t)
	ctx := context.Background()

	a := &model.Agent{Name: "ma", Type: "custom", Image: "img"}
	b := &model.Agent{Name: "mb", Type: "custom", Image: "img"}
	for _, x := range []*model.Agent{a, b} {
		if _, err := svc.Create(ctx, x, "test"); err != nil {
			t.Fatal(err)
		}
	}
	memA, err := st.GetMemoryByAgent(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	memB, err := st.GetMemoryByAgent(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Second writer on memA must be rejected while a owns it.
	if _, err := svc.AttachMemory(ctx, b.Name, memA.ID, "test"); !errors.Is(err, ErrMemoryInUse) {
		t.Fatalf("second writer must be rejected, got %v", err)
	}
	// An agent cannot hold two memories at once.
	if _, err := svc.AttachMemory(ctx, a.Name, memB.ID, "test"); !errors.Is(err, ErrMemoryInUse) {
		t.Fatalf("second memory per agent must be rejected, got %v", err)
	}
	// Running agent: attach/detach refused (fake: not running -> ok below).
	// Detach keeps the data on disk.
	if err := os.WriteFile(filepath.Join(mm.Path(memA.ID), "notes.md"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	det, err := svc.DetachMemory(ctx, a.Name, "test")
	if err != nil {
		t.Fatal(err)
	}
	if det.ID != memA.ID || det.OwnerAgentID != "" {
		t.Fatalf("detach result wrong: %+v", det)
	}
	if b, _ := os.ReadFile(filepath.Join(mm.Path(memA.ID), "notes.md")); string(b) != "keep me" {
		t.Fatal("detach must not touch the data")
	}

	// Replacement agent: free b's default memory, then attach memA.
	if _, err := svc.DetachMemory(ctx, b.Name, "test"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.AttachMemory(ctx, b.Name, memA.ID, "test")
	if err != nil {
		t.Fatalf("attach to replacement agent: %v", err)
	}
	if got.OwnerAgentID != b.ID {
		t.Fatalf("owner after attach: %+v", got)
	}
	// Data survived the migration.
	if bb, _ := os.ReadFile(filepath.Join(mm.Path(memA.ID), "notes.md")); string(bb) != "keep me" {
		t.Fatal("migration must not lose data")
	}
	// Audit trail covers attach and detach.
	if !hasEvent(t, st, model.EventMemoryAttached) || !hasEvent(t, st, model.EventMemoryDetached) {
		t.Fatal("attach/detach must be audited")
	}
	// The detached memB is free for reuse.
	if _, err := svc.AttachMemory(ctx, a.Name, memB.ID, "test"); err != nil {
		t.Fatalf("rebind freed memory: %v", err)
	}
}

func TestEnsureMemoryIdentitiesMigratesLegacyDirs(t *testing.T) {
	svc, fm, _, st, mm := setupWithMemory(t)
	ctx := context.Background()
	a := &model.Agent{ID: model.NewID("ag"), Name: "legacy", Type: "custom", Image: "img"}
	if err := st.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	// Legacy layout: agents/<id>/memory with data, no memory record.
	legacy := fm.MemoryDir(a.ID)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "db.sqlite"), []byte("legacy data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.EnsureMemoryIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	mem, err := st.GetMemoryByAgent(ctx, a.ID)
	if err != nil {
		t.Fatalf("identity not created: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mm.Path(mem.ID), "db.sqlite"))
	if err != nil || string(got) != "legacy data" {
		t.Fatalf("legacy data not migrated: %q %v", got, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy dir should have been moved away")
	}
	// Idempotent: second run changes nothing.
	if err := svc.EnsureMemoryIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListMemories(ctx)
	if len(list) != 1 {
		t.Fatalf("expected exactly one memory identity, got %d", len(list))
	}
}
