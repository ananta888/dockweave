package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestTrueReworkPreservesRouting(t *testing.T) {
	e := baseEdge()
	e.OnTrue, e.ReasonTrue = "rework", "revise successful output"
	st := &fakeStore{edges: []*model.PipelineEdge{e}, runs: map[string][]*model.GateRun{ws: {passRun("r", "fp", time.Minute)}}}
	got := eval(st, "fp", nil).Admission(context.Background(), "b")
	if got.Allowed || got.Action != "rework" || got.Edges[0].Reason != e.ReasonTrue {
		t.Fatalf("routing lost: %+v %+v", got, got.Edges)
	}
}

func TestWildcardOverrideConsumesOnceForMultipleEdges(t *testing.T) {
	e1, e2 := baseEdge(), baseEdge()
	e2.ID, e2.GateID = "e2", "lint"
	st := &fakeStore{edges: []*model.PipelineEdge{e1, e2}, overrides: []*model.PipelineOverride{{
		ID: "wild", AgentID: "b", GateID: "*", SingleUse: true, ExpiresAt: now.Add(time.Hour),
	}}}
	got := eval(st, "", nil).Admit(context.Background(), "b")
	if !got.Allowed || !st.consumed["wild"] {
		t.Fatalf("first work blocked: %+v", got)
	}
}

func TestPhaseSetupFailureDoesNotReuseOldPass(t *testing.T) {
	for _, status := range []model.GateRunStatus{model.GateError, model.GateFailed} {
		t.Run(string(status), func(t *testing.T) {
			bad := &model.GateRun{ID: "bad", Workspace: ws, Phases: []string{"after_run"}, Status: status, Reason: "setup failed", FinishedAt: now}
			st := &fakeStore{edges: []*model.PipelineEdge{baseEdge()}, runs: map[string][]*model.GateRun{ws: {bad, passRun("old", "fp", time.Minute)}}}
			got := eval(st, "fp", nil).Admission(context.Background(), "b")
			if got.Allowed || got.Edges[0].RunID != "bad" {
				t.Fatalf("old PASS reused: %+v", got)
			}
		})
	}
}
