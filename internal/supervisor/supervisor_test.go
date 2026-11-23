package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

type fakeEngine struct {
	containers map[string]string // agentID -> containerID
	states     map[string]string // containerID -> engine state
	findErr    error
}

func (f *fakeEngine) FindByAgent(_ context.Context, agentID string) (string, error) {
	if f.findErr != nil {
		return "", f.findErr
	}
	return f.containers[agentID], nil
}

func (f *fakeEngine) State(_ context.Context, cid string) (string, error) {
	st, ok := f.states[cid]
	if !ok {
		return "", errors.New("no such container")
	}
	return st, nil
}

type fakeMounts struct {
	prepareErr   error
	reconcileErr error
	result       *mounts.ReconcileResult
	actual       []model.MountState

	prepared  []string
	reconcile [][]model.MountState
}

func (f *fakeMounts) PrepareAgentTree(agentID string) error {
	f.prepared = append(f.prepared, agentID)
	return f.prepareErr
}

func (f *fakeMounts) Reconcile(_ string, desired []model.MountState) (*mounts.ReconcileResult, error) {
	f.reconcile = append(f.reconcile, desired)
	if f.result == nil {
		f.result = &mounts.ReconcileResult{Failed: map[string]string{}}
	}
	return f.result, f.reconcileErr
}

func (f *fakeMounts) List(string) ([]model.MountState, error) { return f.actual, nil }

func openStore(t *testing.T) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func addAgent(t *testing.T, s *persistence.Store, name string, state model.AgentState, cid string) *model.Agent {
	t.Helper()
	a := &model.Agent{
		ID: model.NewID("ag"), Name: name, Type: "custom", Image: "ubuntu:24.04",
		AgentRO: true, NetworkMode: "bridge", State: state, ContainerID: cid,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

func addMount(t *testing.T, s *persistence.Store, agentID, target string, st model.MountStatus) {
	t.Helper()
	m := &model.MountState{
		ID: model.NewID("mt"), AgentID: agentID, HostPath: "/src/" + target,
		TargetPath: "/tree/" + agentID + "/" + target, Mode: model.ModeRO,
		Status: st, UpdatedAt: time.Now(),
	}
	if err := s.UpsertMount(context.Background(), m); err != nil {
		t.Fatalf("upsert mount: %v", err)
	}
}

func agentState(t *testing.T, s *persistence.Store, id string) (model.AgentState, string) {
	t.Helper()
	a, err := s.GetAgent(context.Background(), id)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	return a.State, a.ContainerID
}

func mountStatuses(t *testing.T, s *persistence.Store, agentID string) map[string]model.MountStatus {
	t.Helper()
	ms, err := s.ListMounts(context.Background(), agentID)
	if err != nil {
		t.Fatalf("list mounts: %v", err)
	}
	out := map[string]model.MountStatus{}
	for _, m := range ms {
		out[filepath.Base(m.TargetPath)] = m.Status
	}
	return out
}

func recoveryEvents(t *testing.T, s *persistence.Store, agentID string) []string {
	t.Helper()
	evs, err := s.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var out []string
	for _, e := range evs {
		if e.Type == model.EventRecovery && e.AgentID == agentID {
			out = append(out, e.Message)
		}
	}
	return out
}

func TestRecoverMapsEngineStates(t *testing.T) {
	cases := []struct {
		engine string
		want   model.AgentState
	}{
		{"running", model.AgentRunning},
		{"exited", model.AgentExited},
		{"dead", model.AgentExited},
		{"created", model.AgentCreated},
		{"paused", model.AgentStopped},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			s := openStore(t)
			a := addAgent(t, s, "a-"+tc.engine, model.AgentCreated, "")
			if tc.want == model.AgentCreated {
				// Make the transition observable: start from a different state.
				if err := s.UpdateAgentState(context.Background(), a.ID, model.AgentStopped, ""); err != nil {
					t.Fatal(err)
				}
			}
			eng := &fakeEngine{
				containers: map[string]string{a.ID: "cid-1"},
				states:     map[string]string{"cid-1": tc.engine},
			}
			sup := &Supervisor{Store: s, Mounts: &fakeMounts{}, Docker: eng}
			if err := sup.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}
			st, cid := agentState(t, s, a.ID)
			if st != tc.want || cid != "cid-1" {
				t.Fatalf("state=%q cid=%q, want %q cid-1", st, cid, tc.want)
			}
		})
	}
}

func TestRecoverMarksRunningAgentWithoutContainerExited(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "ghost", model.AgentRunning, "old-cid")
	sup := &Supervisor{Store: s, Mounts: &fakeMounts{}, Docker: &fakeEngine{}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, cid := agentState(t, s, a.ID); st != model.AgentExited || cid != "" {
		t.Fatalf("state=%q cid=%q, want exited and cleared container id", st, cid)
	}
}

func TestRecoverKeepsStateWhenEngineLookupFails(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "flaky", model.AgentRunning, "cid-9")
	fm := &fakeMounts{}
	sup := &Supervisor{Store: s, Mounts: fm, Docker: &fakeEngine{findErr: errors.New("engine down")}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	// An engine outage must not be mistaken for "container gone".
	if st, cid := agentState(t, s, a.ID); st != model.AgentRunning || cid != "cid-9" {
		t.Fatalf("state=%q cid=%q, want unchanged running/cid-9", st, cid)
	}
	// Mount recovery still runs.
	if len(fm.prepared) != 1 || len(fm.reconcile) != 1 {
		t.Fatalf("prepared=%v reconcile calls=%d, want 1/1", fm.prepared, len(fm.reconcile))
	}
}

func TestRecoverPrepareFailureRecordsEventAndSkipsReconcile(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "broken-tree", model.AgentCreated, "")
	addMount(t, s, a.ID, "proj", model.MountActive)
	fm := &fakeMounts{prepareErr: errors.New("not shared")}
	sup := &Supervisor{Store: s, Mounts: fm, Docker: &fakeEngine{}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fm.reconcile) != 0 {
		t.Fatalf("reconcile must not run when the tree could not be prepared")
	}
	evs := recoveryEvents(t, s, a.ID)
	if len(evs) != 1 || !strings.Contains(evs[0], "prepare tree failed: not shared") {
		t.Fatalf("events=%v, want one prepare-failure event", evs)
	}
	// Registry is left untouched: nothing was verified.
	if got := mountStatuses(t, s, a.ID)["proj"]; got != model.MountActive {
		t.Fatalf("mount status=%q, want unchanged %q", got, model.MountActive)
	}
}

func TestRecoverReconcilesMountsAndUpdatesRegistry(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "worker", model.AgentCreated, "")
	addMount(t, s, a.ID, "present", model.MountDesired) // in kernel -> active
	addMount(t, s, a.ID, "missing", model.MountActive)  // not in kernel -> desired
	addMount(t, s, a.ID, "failed", model.MountFailed)   // not in kernel -> stays failed
	addMount(t, s, a.ID, "removed", model.MountGone)    // intentionally gone

	tree := "/tree/" + a.ID + "/"
	fm := &fakeMounts{
		actual: []model.MountState{
			{TargetPath: tree + "present"},
			{TargetPath: tree + "foreign"},
		},
		result: &mounts.ReconcileResult{
			Orphaned: []string{tree + "foreign"},
			Failed:   map[string]string{tree + "missing": "source vanished"},
		},
	}
	sup := &Supervisor{Store: s, Mounts: fm, Docker: &fakeEngine{}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(fm.reconcile) != 1 {
		t.Fatalf("reconcile calls=%d, want 1", len(fm.reconcile))
	}
	for _, d := range fm.reconcile[0] {
		if d.Status == model.MountGone {
			t.Fatalf("gone mount %s must not be passed as desired", d.TargetPath)
		}
	}
	if n := len(fm.reconcile[0]); n != 3 {
		t.Fatalf("desired mounts passed=%d, want 3 (gone excluded)", n)
	}

	want := map[string]model.MountStatus{
		"present": model.MountActive,
		"missing": model.MountDesired,
		"failed":  model.MountFailed,
		"removed": model.MountGone,
	}
	got := mountStatuses(t, s, a.ID)
	for k, w := range want {
		if got[k] != w {
			t.Errorf("mount %s: status=%q, want %q", k, got[k], w)
		}
	}
	// The foreign mount is reported, never adopted into the registry.
	if _, ok := got["foreign"]; ok {
		t.Errorf("orphaned mount must not be written to the registry")
	}

	evs := recoveryEvents(t, s, a.ID)
	if len(evs) != 1 || !strings.Contains(evs[0], "orphaned=["+tree+"foreign]") ||
		!strings.Contains(evs[0], "source vanished") {
		t.Fatalf("events=%v, want one event reporting the orphan and the failure", evs)
	}
}

func TestRecoverReconcileErrorLeavesRegistryUntouched(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "err", model.AgentCreated, "")
	addMount(t, s, a.ID, "proj", model.MountActive)
	fm := &fakeMounts{reconcileErr: errors.New("mountinfo unreadable")}
	sup := &Supervisor{Store: s, Mounts: fm, Docker: &fakeEngine{}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mountStatuses(t, s, a.ID)["proj"]; got != model.MountActive {
		t.Fatalf("status=%q, want unchanged %q", got, model.MountActive)
	}
}

func TestRecoverQuietWhenNothingChanged(t *testing.T) {
	s := openStore(t)
	a := addAgent(t, s, "steady", model.AgentCreated, "")
	sup := &Supervisor{Store: s, Mounts: &fakeMounts{}, Docker: &fakeEngine{}}
	if err := sup.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evs := recoveryEvents(t, s, a.ID); len(evs) != 0 {
		t.Fatalf("events=%v, want none for a clean recovery", evs)
	}
}
