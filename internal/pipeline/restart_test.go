package pipeline

import (
	"context"
	"path/filepath"
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
