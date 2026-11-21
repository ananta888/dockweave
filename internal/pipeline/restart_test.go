package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// RF-012: a run whose start marker was written but whose completion never
// landed (daemon crash) keeps blocking after the database is reopened, even
// though an older PASS exists.
func TestIncompleteRunKeepsBlockingAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	ctx := context.Background()
	st, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{{ID: "e", GateID: "tests", UpstreamAgentID: "a",
		DownstreamAgentID: "b", Workspace: ws, OnTrue: "allow", OnFalse: "deny"}}, false); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Minute)
	if err := st.SaveGateRun(ctx, &model.GateRun{ID: "pass", Workspace: ws, Status: model.GatePassed, StartedAt: old,
		FinishedAt: old, WorkspaceFingerprint: "fp", Results: []model.GateResult{{ID: "tests", Status: "passed"}}}); err != nil {
		t.Fatal(err)
	}
	// Start marker as the runner writes it before preparing the sandbox.
	if err := st.SaveGateRun(ctx, &model.GateRun{ID: "crashed", Workspace: ws, Status: model.GateError,
		StartedAt: time.Now().UTC(), Phases: []string{"after_run"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	ev := &Evaluator{Store: reopened, Fingerprint: func(context.Context, string) (string, error) { return "fp", nil }}
	adm := ev.Admission(ctx, "b")
	if adm.Allowed || len(adm.Edges) != 1 || adm.Edges[0].RunID != "crashed" || adm.Edges[0].Code != model.AdmitError {
		t.Fatalf("incomplete run did not block after restart: %+v", adm.Edges)
	}
}

// RF-015: when retention removed the evidence, the block says so instead of
// looking like a gate that never ran. Still blocked either way.
func TestRetentionLossIsReportedDistinctly(t *testing.T) {
	ctx := context.Background()
	st, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{{ID: "e", GateID: "tests", UpstreamAgentID: "a",
		DownstreamAgentID: "b", Workspace: ws, OnTrue: "allow", OnFalse: "deny"}}, false); err != nil {
		t.Fatal(err)
	}
	ev := &Evaluator{Store: st, Fingerprint: func(context.Context, string) (string, error) { return "fp", nil }}
	never := ev.Admission(ctx, "b").Edges[0]
	if never.Code != model.AdmitNoRun || strings.Contains(never.Message, "retention") {
		t.Fatalf("never ran: %+v", never)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := st.SaveGateRun(ctx, &model.GateRun{ID: "old", Workspace: ws, Status: model.GatePassed, StartedAt: old,
		FinishedAt: old, WorkspaceFingerprint: "fp", Phases: []string{"after_run"},
		Results: []model.GateResult{{ID: "tests", Status: "passed"}}}); err != nil {
		t.Fatal(err)
	}
	// A newer run of another gate keeps the workspace in retention (keep=1),
	// so pruning removes the only "tests" evidence.
	newer := time.Now().UTC().Add(-2 * time.Hour)
	if err := st.SaveGateRun(ctx, &model.GateRun{ID: "other", Workspace: ws, Status: model.GatePassed, StartedAt: newer,
		FinishedAt: newer, WorkspaceFingerprint: "fp", Phases: []string{"after_run"},
		Results: []model.GateResult{{ID: "lint", Status: "passed"}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PruneGateRuns(ctx, time.Now().UTC().Add(-time.Hour), 1); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	pruned := ev.Admission(ctx, "b").Edges[0]
	if pruned.Code != model.AdmitNoRun || pruned.Satisfied || !strings.Contains(pruned.Message, "retention") {
		t.Fatalf("retention loss not reported: %+v", pruned)
	}
}

// RF-013 relevance matrix against the real store: which run decides an edge.
func TestRelevanceMatrixWithRealStore(t *testing.T) {
	type run struct {
		id      string
		status  model.GateRunStatus
		phases  []string
		results []model.GateResult
		tamper  bool
		open    bool // start marker only
	}
	pass := func(id string) model.GateResult { return model.GateResult{ID: id, Status: "passed"} }
	fail := func(id string) model.GateResult { return model.GateResult{ID: id, Status: "failed"} }
	for _, tc := range []struct {
		name   string
		newer  run
		gate   string
		code   string
		fromID string
	}{
		{"other phase, other gate: older PASS still decides", run{"n", model.GatePassed, []string{"after_run"}, []model.GateResult{pass("tests")}, false, false}, "lint", model.AdmitOK, "old"},
		{"same gate failed", run{"n", model.GateFailed, []string{"post_run"}, []model.GateResult{fail("lint")}, false, false}, "lint", model.AdmitFailed, "n"},
		{"setup error before any gate", run{"n", model.GateError, []string{"after_run"}, nil, false, false}, "lint", model.AdmitError, "n"},
		{"tamper in another phase", run{"n", model.GateFailed, []string{"after_run"}, []model.GateResult{pass("tests")}, true, false}, "lint", model.AdmitTamper, "n"},
		{"unfinished run", run{"n", model.GateError, []string{"after_run"}, nil, false, true}, "lint", model.AdmitError, "n"},
		{"unrestricted run without the gate", run{"n", model.GatePassed, nil, []model.GateResult{pass("tests")}, false, false}, "lint", model.AdmitUnknownGate, "n"},
		{"workspace policy failure in another phase", run{"n", model.GateFailed, []string{"after_run"}, []model.GateResult{pass("tests")}, false, false}, "lint", model.AdmitError, "n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{{ID: "e", GateID: tc.gate, UpstreamAgentID: "a",
				DownstreamAgentID: "b", Workspace: ws, OnTrue: "allow", OnFalse: "deny"}}, false); err != nil {
				t.Fatal(err)
			}
			old := time.Now().UTC().Add(-time.Hour)
			if err := st.SaveGateRun(ctx, &model.GateRun{ID: "old", Workspace: ws, Status: model.GatePassed, StartedAt: old, FinishedAt: old,
				WorkspaceFingerprint: "fp", Phases: []string{"post_run"}, Results: []model.GateResult{pass("lint")}}); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Add(-time.Minute)
			r := &model.GateRun{ID: tc.newer.id, Workspace: ws, Status: tc.newer.status, StartedAt: at, FinishedAt: at,
				WorkspaceFingerprint: "fp", Phases: tc.newer.phases, Results: tc.newer.results, Tamper: tc.newer.tamper, Reason: "policy: new dependency"}
			if tc.newer.open {
				r.FinishedAt = time.Time{}
			}
			if err := st.SaveGateRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			ev := &Evaluator{Store: st, Fingerprint: func(context.Context, string) (string, error) { return "fp", nil }}
			e := ev.Admission(ctx, "b").Edges[0]
			if e.Code != tc.code || e.RunID != tc.fromID {
				t.Fatalf("got code=%s run=%s (%s), want code=%s run=%s", e.Code, e.RunID, e.Message, tc.code, tc.fromID)
			}
		})
	}
}
