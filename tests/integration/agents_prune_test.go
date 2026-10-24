//go:build integration
// +build integration

package integration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

// pruneDockerStub satisfies agents.DockerOps without an engine.
type pruneDockerStub struct{}

func (pruneDockerStub) EnsureImage(context.Context, string, io.Writer) error  { return nil }
func (pruneDockerStub) Create(context.Context, dockerclient.ContainerSpec) (string, error) {
	return "", nil
}
func (pruneDockerStub) Start(context.Context, string) error     { return nil }
func (pruneDockerStub) Stop(context.Context, string, int) error { return nil }
func (pruneDockerStub) Remove(context.Context, string, bool) error {
	return nil
}
func (pruneDockerStub) FindByAgent(context.Context, string) (string, error) { return "", nil }
func (pruneDockerStub) State(context.Context, string) (string, error)      { return "", nil }
func (pruneDockerStub) ExecOutput(context.Context, string, []string) (string, error) {
	return "", nil
}

// TestAgentPruneBusyMount exercises agents.Remove with a real busy mount:
// prune must report errors (no silent MNT_DETACH), keep the agent record
// (retryable), and succeed on retry. See todo/review-2026-10.json review-003.
func TestAgentPruneBusyMount(t *testing.T) {
	requireRoot(t)
	root := t.TempDir()
	allowed := filepath.Join(root, "projects")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := mounts.New(mounts.Config{Root: root, AllowedRoots: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := persistence.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := &agents.Service{Store: st, Mounts: mgr, Docker: pruneDockerStub{}}
	ctx := context.Background()

	a := &model.Agent{
		ID: "ag_prune", Name: "prune-test", Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := mgr.PrepareAgentTree(a.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = mgr.TeardownAgentTree(a.ID, true) })

	src := filepath.Join(allowed, "proj")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	target, err := mgr.Add(a.ID, src, "proj", model.ModeRW)
	if err != nil {
		t.Fatal(err)
	}

	// Keep a file open: the mount is busy for umount(2).
	f, err := os.Open(filepath.Join(target, "f"))
	if err != nil {
		t.Fatal(err)
	}

	// Prune without force: must fail with the target named, keep the record,
	// and must NOT have used MNT_DETACH.
	err = svc.Remove(ctx, a.ID, "test", true, false)
	if err == nil {
		t.Fatal("busy prune must fail")
	}
	if !strings.Contains(err.Error(), "proj") {
		t.Fatalf("error must name the busy target, got: %v", err)
	}
	if _, err := st.GetAgent(ctx, a.ID); err != nil {
		t.Fatalf("agent record must be kept after failed prune: %v", err)
	}
	evs, _ := st.ListEvents(ctx, 100)
	for _, e := range evs {
		if e.Type == model.EventUnmountForced {
			t.Fatal("no forced detach expected without force flag")
		}
	}
	// Mount is still there.
	list, err := mgr.List(a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("mount must survive failed prune: %v %v", list, err)
	}

	// Release the busy file and retry: clean prune succeeds.
	f.Close()
	if err := svc.Remove(ctx, a.ID, "test", true, false); err != nil {
		t.Fatalf("retry prune: %v", err)
	}
	if _, err := st.GetAgent(ctx, a.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent must be gone after clean prune: %v", err)
	}
}

// TestAgentPruneForceDetach verifies that force prune of a busy mount uses
// an audited MNT_DETACH escalation and completes.
func TestAgentPruneForceDetach(t *testing.T) {
	requireRoot(t)
	root := t.TempDir()
	allowed := filepath.Join(root, "projects")
	os.MkdirAll(allowed, 0o755)
	mgr, err := mounts.New(mounts.Config{Root: root, AllowedRoots: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := persistence.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := &agents.Service{Store: st, Mounts: mgr, Docker: pruneDockerStub{}}
	ctx := context.Background()

	a := &model.Agent{
		ID: "ag_force", Name: "force-test", Type: "custom", Image: "img",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := mgr.PrepareAgentTree(a.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = mgr.TeardownAgentTree(a.ID, true) })

	src := filepath.Join(allowed, "proj")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	target, err := mgr.Add(a.ID, src, "proj", model.ModeRW)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(target, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := svc.Remove(ctx, a.ID, "test", true, true); err != nil {
		t.Fatalf("force prune: %v", err)
	}
	if _, err := st.GetAgent(ctx, a.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("agent must be gone: %v", err)
	}
	evs, _ := st.ListEvents(ctx, 100)
	var forced bool
	for _, e := range evs {
		if e.Type == model.EventUnmountForced {
			forced = true
		}
	}
	if !forced {
		t.Fatal("MNT_DETACH escalation must be audited")
	}
}
