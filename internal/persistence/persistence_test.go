package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testAgent(name string) *model.Agent {
	return &model.Agent{
		ID: model.NewID("ag"), Name: name, Type: "custom", Image: "ubuntu:24.04",
		AgentRO: true, NetworkMode: "bridge", State: model.AgentCreated,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func TestAgentCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a := testAgent("codex-01")
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Duplicate name must conflict.
	dup := testAgent("codex-01")
	if err := s.CreateAgent(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	got, err := s.GetAgent(ctx, "codex-01")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("id mismatch: %s != %s", got.ID, a.ID)
	}
	if err := s.UpdateAgentState(ctx, a.ID, model.AgentRunning, "cid123"); err != nil {
		t.Fatalf("update state: %v", err)
	}
	got, _ = s.GetAgent(ctx, a.ID)
	if got.State != model.AgentRunning || got.ContainerID != "cid123" {
		t.Fatalf("state not updated: %+v", got)
	}
	list, err := s.ListAgents(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if err := s.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestWorkspaceAndGrants(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	w := &model.Workspace{ID: model.NewID("ws"), Name: "proj-a", HostPath: "/srv/a", CreatedAt: time.Now()}
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := s.CreateWorkspace(ctx, &model.Workspace{ID: model.NewID("ws"), Name: "proj-a", HostPath: "/x", CreatedAt: time.Now()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ws name conflict")
	}
	a := testAgent("a1")
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	g := &model.Grant{ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID, Mode: model.ModeRW, CreatedAt: time.Now(), CreatedBy: "test"}
	if err := s.CreateGrant(ctx, g); err != nil {
		t.Fatalf("grant: %v", err)
	}
	// Second active grant for same pair must conflict (partial unique index).
	g2 := &model.Grant{ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID, Mode: model.ModeRO, CreatedAt: time.Now()}
	if err := s.CreateGrant(ctx, g2); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected grant conflict, got %v", err)
	}
	if err := s.RevokeGrant(ctx, g.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// After revoke, a new grant for the pair is allowed.
	if err := s.CreateGrant(ctx, g2); err != nil {
		t.Fatalf("grant after revoke: %v", err)
	}
}

func TestMountUpsertAndDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	m := &model.MountState{
		ID: model.NewID("mnt"), AgentID: "ag_1", GrantID: "gr_1",
		HostPath: "/srv/a", TargetPath: "/var/lib/dockweave/agents/ag_1/workspace/proj-a",
		Mode: model.ModeRO, Status: model.MountActive, UpdatedAt: time.Now(),
	}
	if err := s.UpsertMount(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m.Status = model.MountFailed
	m.LastError = "busy"
	if err := s.UpsertMount(ctx, m); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	list, err := s.ListMounts(ctx, "ag_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].Status != model.MountFailed || list[0].LastError != "busy" {
		t.Fatalf("upsert did not update: %+v", list[0])
	}
	if err := s.DeleteMount(ctx, "ag_1", m.TargetPath); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteMount(ctx, "ag_1", m.TargetPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestEventHashChain(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i, msg := range []string{"one", "two", "three"} {
		e := &model.Event{ID: model.NewID("ev"), Type: model.EventAgentCreated, Actor: "test", Message: msg, Timestamp: time.Now().Add(time.Duration(i) * time.Second)}
		if err := s.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	seq, err := s.VerifyEventChain(ctx)
	if err != nil || seq != 0 {
		t.Fatalf("verify intact chain: seq=%d err=%v", seq, err)
	}
	// Tamper with the log; verification must detect it.
	if _, err := s.db.Exec(`UPDATE events SET message='tampered' WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	seq, err = s.VerifyEventChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("tampering not detected (seq=%d)", seq)
	}
}

func TestGrantExpiry(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	g := &model.Grant{Status: model.GrantActive, ExpiresAt: &future}
	if g.Expired(time.Now()) || !g.Active(time.Now()) {
		t.Fatal("future expiry should be active")
	}
	if !g.Pending(time.Now()) {
		t.Fatal("future expiry should be pending")
	}
	g.ExpiresAt = &past
	if !g.Expired(time.Now()) || g.Active(time.Now()) {
		t.Fatal("past expiry should be inactive")
	}
	if g.Pending(time.Now()) {
		t.Fatal("past expiry should not be pending")
	}
	// A failed creation must never read as active but may claim the pair
	// only in its own lifetime; mount_failed is terminal-failed: not pending.
	g2 := &model.Grant{Status: model.GrantMountFailed}
	if g2.Active(time.Now()) || g2.Pending(time.Now()) {
		t.Fatal("mount_failed grant must be neither active nor pending")
	}
	// unmount_failed: not active (revoke requested) but still claims the pair.
	g3 := &model.Grant{Status: model.GrantUnmountFailed}
	if g3.Active(time.Now()) || !g3.Pending(time.Now()) {
		t.Fatal("unmount_failed grant must be pending but not active")
	}
}

func TestMemorySingleWriter(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	m1 := &model.Memory{ID: "mem1", CreatedAt: now}
	m2 := &model.Memory{ID: "mem2", CreatedAt: now}
	if err := s.CreateMemory(ctx, m1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMemory(ctx, m2); err != nil {
		t.Fatal(err)
	}
	// Bind mem1 to agent a1: ok.
	if err := s.SetMemoryOwner(ctx, "mem1", "a1"); err != nil {
		t.Fatal(err)
	}
	// A second memory for the same agent is rejected (agent -> one memory).
	if err := s.SetMemoryOwner(ctx, "mem2", "a1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second memory for same agent must conflict, got %v", err)
	}
	// Stealing ownership of a bound memory is rejected (memory -> one writer).
	if err := s.SetMemoryOwner(ctx, "mem1", "a2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second writer must conflict, got %v", err)
	}
	// Detach frees the memory and the agent slot.
	if err := s.SetMemoryOwner(ctx, "mem1", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMemory(ctx, "mem1")
	if err != nil || got.OwnerAgentID != "" {
		t.Fatalf("detach: %+v %v", got, err)
	}
	if err := s.SetMemoryOwner(ctx, "mem1", "a2"); err != nil {
		t.Fatalf("rebind after detach: %v", err)
	}
	// Lookup by owner.
	byAgent, err := s.GetMemoryByAgent(ctx, "a2")
	if err != nil || byAgent.ID != "mem1" {
		t.Fatalf("GetMemoryByAgent: %+v %v", byAgent, err)
	}
}
