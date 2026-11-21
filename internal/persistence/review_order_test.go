package persistence

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// RF-016: >500 runs with whole-second, .1, .12, .9 and nanosecond fractions
// (inserted in random order) come back strictly newest first before LIMIT,
// and retention keeps exactly the newest N.
func TestGateHistoryOrderAndRetentionWithManyMixedTimestamps(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	fractions := []time.Duration{0, 100 * time.Millisecond, 120 * time.Millisecond, 900 * time.Millisecond, 1}
	type run struct {
		id string
		at time.Time
	}
	var runs []run
	for i := 0; i < 520; i++ {
		at := base.Add(time.Duration(i/len(fractions)) * time.Second).Add(fractions[i%len(fractions)])
		runs = append(runs, run{fmt.Sprintf("r%03d", i), at})
	}
	rng := rand.New(rand.NewSource(7))
	rng.Shuffle(len(runs), func(i, j int) { runs[i], runs[j] = runs[j], runs[i] })
	for _, r := range runs {
		if err := s.SaveGateRun(ctx, &model.GateRun{ID: r.id, Workspace: "/ws", Status: model.GatePassed, StartedAt: r.at, FinishedAt: r.at}); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].at.After(runs[j].at) })
	got, err := s.ListGateRuns(ctx, "/ws", 50)
	if err != nil || len(got) != 50 {
		t.Fatalf("list: %d %v", len(got), err)
	}
	for i := range got {
		if got[i].ID != runs[i].id || !got[i].StartedAt.Equal(runs[i].at) {
			t.Fatalf("position %d: got %s@%s want %s@%s", i, got[i].ID, got[i].StartedAt, runs[i].id, runs[i].at)
		}
	}
	if _, err := s.PruneGateRuns(ctx, base.Add(24*time.Hour), 37); err != nil {
		t.Fatal(err)
	}
	kept, err := s.ListGateRuns(ctx, "/ws", 1000)
	if err != nil || len(kept) != 37 {
		t.Fatalf("retention kept %d: %v", len(kept), err)
	}
	for i := range kept {
		if kept[i].ID != runs[i].id {
			t.Fatalf("retention dropped a newer run: kept[%d]=%s want %s", i, kept[i].ID, runs[i].id)
		}
	}
}

// RF-019: ListPipelineEdges itself (not an internal helper) never returns a
// revision together with edges of another revision while a second store on
// the same database keeps replacing the set. Connections are released.
func TestListPipelineEdgesSnapshotUnderConcurrentWrites(t *testing.T) {
	s, path := openPipelineStore(t)
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for rev := 1; ; rev++ {
			select {
			case <-stop:
				return
			default:
			}
			n := 1 + rev%3 // edge count varies with the revision
			var edges []*model.PipelineEdge
			for i := 0; i < n; i++ {
				edges = append(edges, edge(fmt.Sprintf("e%d", i), "tests", fmt.Sprintf("b%d", i)))
			}
			if err := writer.ReplacePipelineEdges(ctx, rev, edges, false); err != nil {
				t.Errorf("write rev %d: %v", rev, err)
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		st, err := s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if st.Revision > 0 && len(st.Edges) != 1+st.Revision%3 {
			t.Fatalf("revision %d with %d edges (mixed snapshot)", st.Revision, len(st.Edges))
		}
		for _, e := range st.Edges {
			if e.SourceRevision != st.Revision {
				t.Fatalf("revision %d contains edge of revision %d", st.Revision, e.SourceRevision)
			}
		}
	}
	close(stop)
	wg.Wait()
	if in := s.db.Stats().InUse; in != 0 {
		t.Fatalf("%d connections still in use after reads", in)
	}
}
