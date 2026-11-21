package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

type countingRuns struct {
	*fakeStore
	calls int
}

func (s *countingRuns) RelevantGateRuns(ctx context.Context, ws, gate string) ([]*model.GateRun, error) {
	s.calls++
	return s.fakeStore.RelevantGateRuns(ctx, ws, gate)
}

func TestBatchCachesOnlyWithinOneRequest(t *testing.T) {
	a, b, c := baseEdge(), baseEdge(), baseEdge()
	b.ID, b.DownstreamAgentID = "e2", "c"
	c.ID, c.DownstreamAgentID, c.Workspace = "e3", "d", "/other"
	st := &countingRuns{fakeStore: &fakeStore{edges: []*model.PipelineEdge{a, b, c}, runs: map[string][]*model.GateRun{ws: {passRun("pass", "fp", time.Minute)}, "/other": {passRun("other", "fp", time.Minute)}}}}
	calls := 0
	fingerprint := "fp"
	ev := &Evaluator{Store: st, Now: func() time.Time { return now }, Fingerprint: func(context.Context, string) (string, error) { calls++; return fingerprint, nil }}
	state := &model.PipelineState{Revision: 9, Edges: st.edges}
	got := ev.Admissions(context.Background(), state)
	if len(got) != 3 || calls != 2 || st.calls != 2 {
		t.Fatalf("work duplicated: fingerprints=%d history=%d admissions=%d", calls, st.calls, len(got))
	}
	for _, a := range got {
		if !a.Allowed || a.Revision != 9 {
			t.Fatalf("batch result: %+v", a)
		}
	}
	fingerprint = "changed"
	if adm := ev.Admit(context.Background(), "b"); adm.Allowed || adm.Edges[0].Code != model.AdmitStale || calls != 3 {
		t.Fatalf("stale request cache reused: %+v calls=%d", adm, calls)
	}
}

func TestCancelledBatchDoesNoFingerprintWork(t *testing.T) {
	st := &fakeStore{edges: []*model.PipelineEdge{baseEdge()}}
	ev := eval(st, "fp", nil)
	ev.Fingerprint = func(context.Context, string) (string, error) {
		t.Fatal("fingerprint called after cancellation")
		return "", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ev.Admissions(ctx, &model.PipelineState{Revision: 1, Edges: st.edges})
	if len(got) != 1 || got[0].Allowed {
		t.Fatalf("cancelled request: %+v", got)
	}
}

func TestFreshnessTimeBoundaries(t *testing.T) {
	for _, c := range []struct {
		name              string
		finished, started time.Time
		age               int
		allowed           bool
	}{
		{"before expiry", now.Add(-59 * time.Second), now.Add(-time.Minute), 60, true},
		{"at expiry", now.Add(-time.Minute), now.Add(-2 * time.Minute), 60, true},
		{"after expiry", now.Add(-time.Minute - time.Nanosecond), now.Add(-2 * time.Minute), 60, false},
		{"missing finish", time.Time{}, now, 0, false},
		{"future", now.Add(time.Minute + time.Nanosecond), now, 0, false},
		{"allowed skew", now.Add(time.Minute), now, 0, true},
		{"inverted", now.Add(-time.Second), now, 0, false},
		{"overflow age", now, now, int(^uint(0) >> 1), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := baseEdge()
			e.MaxAgeSec = c.age
			r := passRun("r", "fp", 0)
			r.FinishedAt, r.StartedAt = c.finished, c.started
			st := &fakeStore{edges: []*model.PipelineEdge{e}, runs: map[string][]*model.GateRun{ws: {r}}}
			if got := eval(st, "fp", nil).Admission(context.Background(), "b"); got.Allowed != c.allowed {
				t.Fatalf("allowed=%v want %v edges=%+v", got.Allowed, c.allowed, got.Edges)
			}
		})
	}
}

func BenchmarkSharedWorkspaceAdmissions(b *testing.B) {
	for _, batch := range []bool{false, true} {
		b.Run(fmt.Sprintf("batch=%t", batch), func(b *testing.B) {
			st := &fakeStore{runs: map[string][]*model.GateRun{ws: {passRun("pass", "fp", time.Minute)}}}
			for i := range 20 {
				e := baseEdge()
				e.ID = fmt.Sprintf("e%d", i)
				e.DownstreamAgentID = fmt.Sprintf("agent%d", i)
				st.edges = append(st.edges, e)
			}
			data := make([]byte, 1<<20)
			ev := eval(st, "fp", nil)
			ev.Fingerprint = func(context.Context, string) (string, error) { _ = sha256.Sum256(data); return "fp", nil }
			state := &model.PipelineState{Revision: 1, Edges: st.edges}
			ctx := context.Background()
			b.ResetTimer()
			for range b.N {
				if batch {
					ev.Admissions(ctx, state)
				} else {
					for _, e := range st.edges {
						ev.Admission(ctx, e.DownstreamAgentID)
					}
				}
			}
		})
	}
}
