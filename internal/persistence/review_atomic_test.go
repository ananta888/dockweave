package persistence

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestOverrideBatchRollsBackWhenOneAuthorizationExpired(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, o := range []*model.PipelineOverride{
		{ID: "live", AgentID: "b", GateID: "tests", SingleUse: true, CreatedAt: at, ExpiresAt: at.Add(time.Hour)},
		{ID: "old", AgentID: "b", GateID: "lint", SingleUse: true, CreatedAt: at, ExpiresAt: at.Add(-time.Hour)},
	} {
		if err := s.CreatePipelineOverride(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ConsumePipelineOverrides(ctx, []string{"live", "old"}, at); err == nil {
		t.Fatal("expired batch allowed")
	}
	list, err := s.ListPipelineOverrides(ctx, "b", at)
	if err != nil || len(list) != 1 || list[0].UsedAt != nil {
		t.Fatalf("partial consumption: %+v %v", list, err)
	}
}

func TestPipelineMutationsRollBackWhenAuditFails(t *testing.T) {
	for _, kind := range []string{"replace", "create", "consume"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := openPipelineStore(t)
			ctx := context.Background()
			at := time.Now().UTC()
			o := &model.PipelineOverride{ID: "o", AgentID: "b", GateID: "*", SingleUse: true, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
			if kind == "consume" {
				if err := s.CreatePipelineOverride(ctx, o); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_audit BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "replace":
				err = s.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{edge("e", "test", "b")}, false)
			case "create":
				err = s.CreatePipelineOverride(ctx, o)
			case "consume":
				err = s.ConsumePipelineOverrides(ctx, []string{"o", "o"}, at)
			}
			if err == nil {
				t.Fatal("unaudited mutation succeeded")
			}
			state, e := s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
			if e != nil || state.Revision != 0 || len(state.Edges) != 0 {
				t.Fatalf("edge mutation escaped rollback: %+v %v", state, e)
			}
			list, e := s.ListPipelineOverrides(ctx, "b", time.Time{})
			if e != nil {
				t.Fatal(e)
			}
			if kind == "create" && len(list) != 0 {
				t.Fatal("override creation escaped rollback")
			}
			if kind == "consume" && (len(list) != 1 || list[0].UsedAt != nil) {
				t.Fatal("consumption escaped rollback")
			}
		})
	}
}

func TestCompleteGateRunIsAtomicAcrossEvidenceAndAudit(t *testing.T) {
	for _, table := range []string{"evidence", "events"} {
		t.Run(table, func(t *testing.T) {
			s, _ := openPipelineStore(t)
			ctx := context.Background()
			at := time.Now().UTC()
			r := &model.GateRun{ID: "r", Workspace: "/ws", StartedAt: at, Status: model.GateError, Phases: []string{"after_run"}, Reason: "incomplete"}
			if err := s.SaveGateRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER fail_completion BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'injected'); END", table)); err != nil {
				t.Fatal(err)
			}
			r.Status = model.GatePassed
			r.FinishedAt = at.Add(time.Second)
			r.Results = []model.GateResult{{ID: "tests", Status: "passed"}}
			ev := &model.Evidence{ID: "ev", Kind: model.EvidenceGate, Status: model.EvidencePassed, Ref: r.ID, Verified: true, IdempotencyKey: "gate-run:r", CreatedAt: r.FinishedAt}
			event := &model.Event{ID: "event", Type: model.EventGateRun, Timestamp: r.FinishedAt}
			if err := s.CompleteGateRun(ctx, r, ev, event); err == nil {
				t.Fatal("injected persistence failure ignored")
			}
			got, err := s.RelevantGateRuns(ctx, "/ws", "tests")
			if err != nil || len(got) != 1 || !got[0].FinishedAt.IsZero() {
				t.Fatalf("incomplete marker lost: %+v %v", got, err)
			}
			var n int
			if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM evidence`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial evidence: %d %v", n, err)
			}
			if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_completion`); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteGateRun(ctx, r, ev, event); err != nil {
				t.Fatal(err)
			}
			got, err = s.RelevantGateRuns(ctx, "/ws", "tests")
			if err != nil || len(got) != 1 || got[0].Status != model.GatePassed {
				t.Fatalf("successful completion missing: %+v %v", got, err)
			}
		})
	}
}

func TestPipelineSnapshotNeverMixesRevisions(t *testing.T) {
	s, path := openPipelineStore(t)
	ctx := context.Background()
	if err := s.ReplacePipelineEdges(ctx, 1, []*model.PipelineEdge{edge("e", "tests", "b")}, false); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if rev, err := pipelineRevision(ctx, tx); err != nil || rev != 1 {
		t.Fatalf("snapshot start: %d %v", rev, err)
	}
	if err := other.ReplacePipelineEdges(ctx, 2, nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := readPipelineEdges(ctx, tx, PipelineEdgeFilter{})
	if err != nil || got.Revision != 1 || len(got.Edges) != 1 || got.Edges[0].SourceRevision != 1 {
		t.Fatalf("mixed snapshot: %+v %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListPipelineEdges(ctx, PipelineEdgeFilter{})
	if err != nil || got.Revision != 2 || len(got.Edges) != 0 {
		t.Fatalf("new empty snapshot missing: %+v %v", got, err)
	}
}

func TestConcurrentDivergentRevisionHasOneWinner(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	wins := make(chan bool, 2)
	var wg sync.WaitGroup
	for _, gate := range []string{"lint", "tests"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			wins <- s.ReplacePipelineEdges(ctx, 7, []*model.PipelineEdge{edge("e", gate, "b")}, false) == nil
		}()
	}
	close(start)
	wg.Wait()
	close(wins)
	count := 0
	for win := range wins {
		if win {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("winners=%d", count)
	}
}
