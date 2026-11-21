package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestSameRevisionCannotReplaceDifferentEdges(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	if err := s.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{edge("e", "tests", "b")}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePipelineEdges(ctx, 1, nil, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent retry accepted: %v", err)
	}
	got, err := s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	if err != nil || len(got.Edges) != 1 {
		t.Fatalf("state lost: %+v %v", got, err)
	}
}

func TestGateHistoryOrdersBeforeLimit(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for id, off := range map[string]time.Duration{"whole": 0, "fraction": time.Millisecond} {
		at := base.Add(off)
		if err := s.SaveGateRun(ctx, &model.GateRun{ID: id, Workspace: "/ws", Status: model.GatePassed, StartedAt: at, FinishedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListGateRuns(ctx, "/ws", 1)
	if err != nil || len(got) != 1 || got[0].ID != "fraction" {
		t.Fatalf("wrong latest run: %+v %v", got, err)
	}
}

// RF-018: repeating the stored revision with identical edges is a no-op:
// no timestamp change and no second audit event.
func TestIdenticalRevisionRepeatChangesNothing(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	edges := []*model.PipelineEdge{edge("e", "tests", "b")}
	ev := func() *model.Event {
		return &model.Event{ID: model.NewID("evt"), Type: model.EventPipelineUpdate, Actor: "t", Timestamp: time.Now().UTC()}
	}
	if err := s.ReplacePipelineEdgesAudited(ctx, 4, edges, false, ev()); err != nil {
		t.Fatal(err)
	}
	first, _ := s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	before, _ := s.ListEvents(ctx, 100)
	time.Sleep(5 * time.Millisecond) // would show up in UpdatedAt if rewritten
	if err := s.ReplacePipelineEdgesAudited(ctx, 4, []*model.PipelineEdge{edge("e", "tests", "b")}, false, ev()); err != nil {
		t.Fatalf("identical repeat: %v", err)
	}
	second, _ := s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	after, _ := s.ListEvents(ctx, 100)
	if !second.UpdatedAt.Equal(first.UpdatedAt) || !second.Edges[0].UpdatedAt.Equal(first.Edges[0].UpdatedAt) {
		t.Fatalf("idempotent repeat rewrote the state: %v -> %v", first.UpdatedAt, second.UpdatedAt)
	}
	if len(after) != len(before) {
		t.Fatalf("idempotent repeat wrote %d audit event(s)", len(after)-len(before))
	}
}
