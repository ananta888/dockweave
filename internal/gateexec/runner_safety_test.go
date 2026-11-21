package gateexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

type callbackUsage struct{ call func() }

func (c callbackUsage) Usage(context.Context, string) (*credentialbroker.Usage, error) {
	c.call()
	return &credentialbroker.Usage{GrantID: "g1", RequestsDay: 1}, nil
}

func TestRunSnapshotRemainsBoundToExecutedConfiguration(t *testing.T) {
	runner, _ := newTestRunner(t, nil)
	initial := `{"version":1,"budget":{"grant_id":"g1","max_requests_day":20},"gates":[{"id":"check","command":"echo original","when":"after_run"}]}`
	workspace := writeWorkspace(t, initial)
	fp := mustFP(t, workspace)
	changed := false
	runner.Broker = callbackUsage{call: func() {
		if !changed {
			changed = true
			writeTree(t, workspace, map[string]string{".paperclip/gates.json": `{"version":1,"gates":[{"id":"check","command":"exit 99"}]}`})
		}
	}}
	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GatePassed || run.GatesHash != hashString([]byte(initial)) || run.WorkspaceFingerprint != fp {
		t.Fatalf("snapshot/config mismatch: %+v", run)
	}
	if run.WorkspaceFingerprint == mustFP(t, workspace) {
		t.Fatal("live replacement incorrectly considered checked")
	}
}

func TestRunPolicyBaselineIgnoresConcurrentLiveChanges(t *testing.T) {
	runner, _ := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"allow_paths":["allowed/"],"budget":{"grant_id":"g1","max_requests_day":20},"gates":[{"id":"check","command":"true"}]}`)
	writeTree(t, workspace, map[string]string{"source": "original"})
	runner.Broker = callbackUsage{call: func() { writeTree(t, workspace, map[string]string{"source": "new live content"}) }}
	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GatePassed || run.Diff == nil || len(run.Diff.Changed) != 0 {
		t.Fatalf("live mutation affected private policy baseline: %+v", run)
	}
}

func TestRunStartPersistenceFailureStopsPreparation(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	workspace := writeWorkspace(t, `{"version":1,"gates":[]}`)
	base := filepath.Join(t.TempDir(), "not-created")
	runner := Runner{Store: store, BaseDir: base}
	if _, err := runner.Run(context.Background(), Request{Workspace: workspace}); err == nil {
		t.Fatal("start persistence failure hidden")
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation started: %v", err)
	}
}

func TestRunCompletionFailureIsReturnedAndStartMarkerWasDurable(t *testing.T) {
	runner, store := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"budget":{"grant_id":"g1","max_requests_day":1},"gates":[{"id":"check","command":"true"}]}`)
	markerSeen := false
	runner.Broker = callbackUsage{call: func() {
		if markerSeen {
			return
		}
		markerSeen = true
		runs, err := store.ListGateRuns(context.Background(), workspace, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 1 || runs[0].Status != model.GateError || !runs[0].FinishedAt.IsZero() {
			t.Fatalf("no durable incomplete run: %+v", runs)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := runner.Run(context.Background(), Request{Workspace: workspace}); err == nil {
		t.Fatal("completion write failure hidden")
	}
	if !markerSeen {
		t.Fatal("start marker never checked")
	}
}

func TestRecordCompletesAfterRequestCancellation(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := Runner{Store: store}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &model.GateRun{ID: "cancelled-run", Workspace: t.TempDir(), Status: model.GateError, Reason: "cancelled", StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	if err := runner.record(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListGateRuns(context.Background(), run.Workspace, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("completion missing: %v %+v", err, runs)
	}
}

// RF-011: an unreadable part of the workspace makes the snapshot/baseline
// incomplete; the run must end as an error, never as PASS.
func TestRunWithUnreadableWorkspaceNeverPasses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict root")
	}
	runner, _ := newTestRunner(t, nil)
	workspace := writeWorkspace(t, `{"version":1,"gates":[{"id":"check","command":"true"}]}`)
	writeTree(t, workspace, map[string]string{"locked/secret.txt": "x"})
	locked := filepath.Join(workspace, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	run, err := runner.Run(context.Background(), Request{Workspace: workspace})
	if run != nil && run.Status == model.GatePassed {
		t.Fatalf("incomplete baseline produced PASS: %+v", run)
	}
	if err == nil && (run == nil || run.Status != model.GateError) {
		t.Fatalf("want an error result, got %+v", run)
	}
}

func TestRunSymlinkPolicyEndToEnd(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(outside, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		policy string
		pass   bool
	}{{"", true}, {"hash", true}, {"ignored", true}, {"fail", false}} {
		t.Run("policy="+tc.policy, func(t *testing.T) {
			runner, _ := newTestRunner(t, nil)
			cfg := `{"version":1,"gates":[{"id":"check","command":"test ! -e node_modules/.bin/tool"}]}`
			if tc.policy != "" {
				cfg = `{"version":1,"symlink_policy":"` + tc.policy + `","gates":[{"id":"check","command":"test ! -e node_modules/.bin/tool"}]}`
			}
			workspace := writeWorkspace(t, cfg)
			if err := os.MkdirAll(filepath.Join(workspace, "node_modules", ".bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(workspace, "node_modules", ".bin", "tool")); err != nil {
				t.Fatal(err)
			}
			run, err := runner.Run(context.Background(), Request{Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			if tc.pass {
				// The link is absent in the sandbox (the gate checks that) and the
				// recorded fingerprint is the live one, links included.
				if run.Status != model.GatePassed || run.WorkspaceFingerprint != mustFP(t, workspace) || len(run.Warnings) == 0 {
					t.Fatalf("want PASS with warning: %+v", run)
				}
			} else if run.Status == model.GatePassed {
				t.Fatalf("fail policy passed: %+v", run)
			}
		})
	}
}
