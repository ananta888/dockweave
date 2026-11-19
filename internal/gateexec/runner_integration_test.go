package gateexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

type staticUsage struct{ usage credentialbroker.Usage }

func (s staticUsage) Usage(context.Context, string) (*credentialbroker.Usage, error) {
	u := s.usage
	return &u, nil
}

func writeWorkspace(t *testing.T, gatesJSON string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".paperclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".paperclip", "gates.json"), []byte(gatesJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x","dependencies":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newTestRunner(t *testing.T, broker UsageChecker) (*Runner, *persistence.Store) {
	t.Helper()
	if os.Getenv("DOCKWEAVE_GATE_INTEGRATION") != "1" {
		t.Skip("set DOCKWEAVE_GATE_INTEGRATION=1 to run Docker-backed gate tests")
	}
	docker, err := dockerclient.New()
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := docker.Ping(ctx); err != nil {
		t.Skipf("docker ping failed: %v", err)
	}
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &Runner{
		Docker:       docker,
		Store:        store,
		Broker:       broker,
		BaseDir:      t.TempDir(),
		DefaultImage: "node:22-bookworm",
	}, store
}

func TestRunSandboxGate(t *testing.T) {
	runner, store := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"gates":[{"id":"smoke","when":"after_run","command":"echo gate-ok","cwd":".","timeout_sec":60}]}`)

	run, err := runner.Run(context.Background(), Request{Workspace: workspace, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GatePassed || run.Passed != 1 || run.Tamper {
		t.Fatalf("unexpected run: %+v", run)
	}
	if len(run.Results) != 1 || run.Results[0].Status != "passed" {
		t.Fatalf("unexpected results: %+v", run.Results)
	}
	if run.ImageDigest == "" {
		t.Fatal("image digest was not recorded")
	}
	runs, err := store.ListGateRuns(context.Background(), workspace, 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("run not persisted: %v %+v", err, runs)
	}
}

func TestRunDetectsTamper(t *testing.T) {
	runner, _ := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"gates":[{"id":"tamper","when":"after_run","command":"echo modified >> package.json","cwd":".","timeout_sec":60}]}`)

	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if !run.Tamper || run.Status != model.GateFailed {
		t.Fatalf("tamper not detected: %+v", run)
	}
}

func TestRunFailsClosedOnNonZeroExit(t *testing.T) {
	runner, _ := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"gates":[{"id":"fail","when":"after_run","command":"exit 3","cwd":".","timeout_sec":60}]}`)

	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GateFailed || run.Results[0].Exit != 3 {
		t.Fatalf("unexpected run: %+v", run)
	}
}

func TestRunRealWorkspace(t *testing.T) {
	workspace := os.Getenv("DOCKWEAVE_GATE_WORKSPACE")
	if workspace == "" {
		t.Skip("set DOCKWEAVE_GATE_WORKSPACE=/path/to/project to run its real gates")
	}
	runner, _ := newTestRunner(t, nil)
	run, err := runner.Run(context.Background(), Request{Workspace: workspace, Actor: "integration", Timeout: 20 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%s passed=%d/%d tamper=%t reason=%s", run.Status, run.Passed, run.Gates, run.Tamper, run.Reason)
	for _, r := range run.Results {
		t.Logf("  %s: %s exit=%d %dms", r.ID, r.Status, r.Exit, r.DurationMS)
	}
	if run.Status != model.GatePassed {
		t.Fatalf("real workspace gates failed: %+v", run.Results)
	}
}

func TestRunBudgetGate(t *testing.T) {
	runner, _ := newTestRunner(t, staticUsage{usage: credentialbroker.Usage{GrantID: "g1", RequestsDay: 10}})
	workspace := writeWorkspace(t, `{"version":1,"budget":{"grant_id":"g1","max_requests_day":5},"gates":[{"id":"smoke","when":"after_run","command":"true","timeout_sec":30}]}`)

	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GateFailed || run.Reason == "" {
		t.Fatalf("budget gate did not block: %+v", run)
	}
	if run.Budget == nil || !run.Budget.Exceeded || run.Budget.RequestsDay != 10 {
		t.Fatalf("budget snapshot missing or wrong: %+v", run.Budget)
	}
}
