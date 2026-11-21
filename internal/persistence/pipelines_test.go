package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func openPipelineStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func edge(id, gate, down string) *model.PipelineEdge {
	return &model.PipelineEdge{ID: id, GateID: gate, UpstreamAgentID: "a", DownstreamAgentID: down,
		Workspace: "/ws", OnTrue: "allow", OnFalse: "rework", ReasonFalse: "fix", MaxAgeSec: 60}
}

func TestPipelineEdgesReplaceAndRevisionGuard(t *testing.T) {
	st, path := openPipelineStore(t)
	ctx := context.Background()

	if err := st.ReplacePipelineEdges(ctx, 3, []*model.PipelineEdge{edge("e1", "tests", "b"), edge("e2", "lint", "c")}, false); err != nil {
		t.Fatal(err)
	}
	state, err := st.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	if err != nil || state.Revision != 3 || len(state.Edges) != 2 {
		t.Fatalf("state = %+v, %v", state, err)
	}
	e := state.Edges[0]
	if e.GateID != "tests" || e.OnFalse != "rework" || e.ReasonFalse != "fix" || e.MaxAgeSec != 60 || e.SourceRevision != 3 || e.UpdatedAt.IsZero() {
		t.Fatalf("edge round trip = %+v", e)
	}

	// Older revision never overwrites a newer one.
	if err := st.ReplacePipelineEdges(ctx, 2, nil, false); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale push err = %v", err)
	}
	// Same revision is an idempotent re-push; newer replaces (removal works).
	if err := st.ReplacePipelineEdges(ctx, 3, []*model.PipelineEdge{edge("e1", "tests", "b"), edge("e2", "lint", "c")}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplacePipelineEdges(ctx, 4, []*model.PipelineEdge{edge("e1", "tests", "b")}, false); err != nil {
		t.Fatal(err)
	}
	edges, rev, err := st.EdgesForDownstream(ctx, "c")
	if err != nil || rev != 4 || len(edges) != 0 {
		t.Fatalf("removed edge still present: %+v rev=%d err=%v", edges, rev, err)
	}
	// Reset allows a restarted revision counter.
	if err := st.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{edge("e9", "tests", "b")}, true); err != nil {
		t.Fatal(err)
	}

	// Survives reopen (migration is not re-applied destructively).
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	edges, rev, err = st2.EdgesForDownstream(ctx, "b")
	if err != nil || rev != 1 || len(edges) != 1 || edges[0].ID != "e9" {
		t.Fatalf("after reopen: %+v rev=%d err=%v", edges, rev, err)
	}
}

func TestPipelineEdgesReplaceIsAtomic(t *testing.T) {
	st, _ := openPipelineStore(t)
	ctx := context.Background()
	if err := st.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{edge("e1", "tests", "b")}, false); err != nil {
		t.Fatal(err)
	}
	// Duplicate (gate, downstream, workspace) fails the whole replace.
	err := st.ReplacePipelineEdges(ctx, 2, []*model.PipelineEdge{edge("x1", "lint", "b"), edge("x2", "lint", "b")}, false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
	state, _ := st.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	if state.Revision != 1 || len(state.Edges) != 1 || state.Edges[0].ID != "e1" {
		t.Fatalf("failed replace must leave the old state: %+v", state)
	}
}

func TestGateRunPipelineColumns(t *testing.T) {
	st, _ := openPipelineStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := &model.GateRun{ID: "g1", Workspace: "/ws", Status: model.GatePassed, StartedAt: now, FinishedAt: now,
		WorkspaceFingerprint: "sha256:x", UpstreamAgentID: "a", Phases: []string{"after_run", "post_run"}}
	if err := st.SaveGateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := st.RecentGateRuns(ctx, "/ws", 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	got := runs[0]
	if got.WorkspaceFingerprint != "sha256:x" || got.UpstreamAgentID != "a" || len(got.Phases) != 2 {
		t.Fatalf("run = %+v", got)
	}
}

func TestRecentGateRunsOrdersByParsedTime(t *testing.T) {
	st, _ := openPipelineStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 21, 10, 0, 5, 0, time.UTC)
	// .1s formats as "05.1Z" and .12s as "05.12Z": lexically "05.1Z" > "05.12Z".
	for id, off := range map[string]time.Duration{"older": 100 * time.Millisecond, "newer": 120 * time.Millisecond} {
		at := base.Add(off)
		if err := st.SaveGateRun(ctx, &model.GateRun{ID: id, Workspace: "/ws", Status: model.GatePassed, StartedAt: at, FinishedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := st.RecentGateRuns(ctx, "/ws", 0)
	if len(runs) != 2 || runs[0].ID != "newer" {
		t.Fatalf("order = %s,%s", runs[0].ID, runs[1].ID)
	}
}

func TestPipelineOverrides(t *testing.T) {
	st, _ := openPipelineStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	o := &model.PipelineOverride{ID: "o1", AgentID: "b", GateID: "tests", Reason: "hotfix", Actor: "root",
		SingleUse: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreatePipelineOverride(ctx, o); err != nil {
		t.Fatal(err)
	}
	expired := &model.PipelineOverride{ID: "o2", AgentID: "b", GateID: "tests", Reason: "old", Actor: "root",
		SingleUse: false, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	if err := st.CreatePipelineOverride(ctx, expired); err != nil {
		t.Fatal(err)
	}
	active, err := st.ListPipelineOverrides(ctx, "b", now)
	if err != nil || len(active) != 1 || active[0].ID != "o1" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	all, _ := st.ListPipelineOverrides(ctx, "", time.Time{})
	if len(all) != 2 {
		t.Fatalf("all = %d", len(all))
	}

	// Concurrent consumption: exactly one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := st.ConsumePipelineOverride(ctx, "o1", time.Now().UTC()); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("single-use override consumed %d times", wins)
	}
	if active, _ := st.ListPipelineOverrides(ctx, "b", time.Now().UTC()); len(active) != 0 {
		t.Fatalf("used override still active: %+v", active)
	}
	if err := st.ConsumePipelineOverride(ctx, "o2", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired override err = %v", err)
	}
	if err := st.ConsumePipelineOverride(ctx, "nope", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing override err = %v", err)
	}
}
