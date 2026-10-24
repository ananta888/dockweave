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
func (f *fakeMounts) AgentDir(id string) string                      { return filepath.Join(f.root, "agents", id, "agent") }
func (f *fakeMounts) MemoryDir(id string) string                     { return filepath.Join(f.root, "agents", id, "memory") }
func (f *fakeMounts) WorkspaceTree(id string) string                 { return filepath.Join(f.root, "agents", id, "workspace") }
func (f *fakeMounts) List(string) ([]model.MountState, error)        { return f.list, nil }

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
func (f *fakeDocker) Start(context.Context, string) error             { return nil }
func (f *fakeDocker) Stop(context.Context, string, int) error         { return nil }
func (f *fakeDocker) Remove(_ context.Context, id string, _ bool) error {
	if f.rmErr != nil {
		return f.rmErr
	}
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeDocker) FindByAgent(context.Context, string) (string, error) { return "cid", nil }
func (f *fakeDocker) State(context.Context, string) (string, error)      { return "exited", nil }
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
