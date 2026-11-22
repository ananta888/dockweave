package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestRelevantGateLookupSurvivesUnrelatedHistoryAndRetention(t *testing.T) {
	s, _ := openPipelineStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	pass := &model.GateRun{ID: "pass", Workspace: "/ws", StartedAt: base, FinishedAt: base, Status: model.GatePassed, Phases: []string{"after_run"}, Results: []model.GateResult{{ID: "tests", Status: "passed"}}}
	if err := s.SaveGateRun(ctx, pass); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 550; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		r := &model.GateRun{ID: fmt.Sprintf("pre-%04d", i), Workspace: "/ws", StartedAt: at, FinishedAt: at, Status: model.GatePassed, Phases: []string{"pre_run"}, Results: []model.GateResult{{ID: "pre", Status: "passed"}}}
		if err := s.SaveGateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RelevantGateRuns(ctx, "/ws", "tests")
	if err != nil || len(got) != 1 || got[0].ID != "pass" {
		t.Fatalf("retained PASS hidden: %+v %v", got, err)
	}
	bad := &model.GateRun{ID: "setup", Workspace: "/ws", StartedAt: base.Add(time.Second), FinishedAt: base.Add(time.Second), Phases: []string{"after_run"}, Status: model.GateFailed, Reason: "budget unavailable"}
	if err := s.SaveGateRun(ctx, bad); err != nil {
		t.Fatal(err)
	}
	got, err = s.RelevantGateRuns(ctx, "/ws", "tests")
	if err != nil || len(got) != 1 || got[0].ID != "setup" {
		t.Fatalf("new setup failure hidden: %+v %v", got, err)
	}
	if _, err := s.PruneGateRuns(ctx, base.Add(2*time.Second), 1); err != nil {
		t.Fatal(err)
	}
	// The global failure is retained; remove it explicitly to distinguish true
	// evidence deletion from a history lookup window.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM gate_runs WHERE id='setup'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.RelevantGateRuns(ctx, "/ws", "tests")
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted evidence still visible: %+v %v", got, err)
	}
}

func TestGateTimestampMigrationPreservesNanosecondsAndRetention(t *testing.T) {
	s, path := openPipelineStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for i, off := range []time.Duration{0, 100 * time.Millisecond, 120 * time.Millisecond, 900 * time.Millisecond, time.Nanosecond} {
		at := base.Add(off)
		id := fmt.Sprint(i)
		if err := s.SaveGateRun(ctx, &model.GateRun{ID: id, Workspace: "/ws", Status: model.GatePassed, StartedAt: at, FinishedAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE gate_runs SET started_at=?,finished_at=? WHERE id=?`, ts(at), ts(at), id); err != nil {
			t.Fatal(err)
		}
	}
	undoTicketMigration(t, s)
	if _, err := s.db.ExecContext(ctx, `DROP TABLE gate_run_relevance; DROP INDEX idx_gate_runs_chronological; UPDATE meta SET value='18' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.ListGateRuns(ctx, "/ws", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"3", "2", "1", "4", "0"}
	if len(got) != len(want) {
		t.Fatalf("rows=%d", len(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d]=%s want %s", i, got[i].ID, id)
		}
	}
	if n, err := s.PruneGateRuns(ctx, base.Add(time.Second), 2); err != nil || n != 3 {
		t.Fatalf("pruned=%d err=%v", n, err)
	}
	got, err = s.ListGateRuns(ctx, "/ws", 5)
	if err != nil || len(got) != 2 || got[0].ID != "3" || got[1].ID != "2" {
		t.Fatalf("newest rows lost: %+v %v", got, err)
	}
}

// Migration 20: existing workspace-wide failures (failed run, no failed
// gate) become relevant to every gate.
func TestMigrationMarksExistingPolicyFailuresGlobal(t *testing.T) {
	s, path := openPipelineStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if err := s.SaveGateRun(ctx, &model.GateRun{ID: "policy", Workspace: "/ws", Status: model.GateFailed, StartedAt: at, FinishedAt: at,
		Phases: []string{"after_run"}, Results: []model.GateResult{{ID: "tests", Status: "passed"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM gate_run_relevance WHERE run_id='policy' AND gate_id=''`); err != nil {
		t.Fatal(err)
	}
	undoTicketMigration(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE meta SET value='19' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runs, err := reopened.RelevantGateRuns(ctx, "/ws", "lint")
	if err != nil || len(runs) != 1 || runs[0].ID != "policy" {
		t.Fatalf("policy failure not relevant to other gates after migration: %+v %v", runs, err)
	}
}

// undoTicketMigration reverts migration 21 so a test can replay the
// migrations after an older schema version.
func undoTicketMigration(t *testing.T, s *Store) {
	t.Helper()
	stmts := []string{"DROP TABLE IF EXISTS dispatch_actions", "DROP TABLE ticket_comments", "DROP INDEX idx_tasks_ticket_seq", "DROP INDEX idx_tasks_parent", "DROP INDEX idx_tasks_inbox"}
	for _, c := range []string{"parent_id", "assignee_id", "assignee_name", "created_by", "version", "checked_out_by", "checked_out_at", "ticket_seq"} {
		stmts = append(stmts, "ALTER TABLE tasks DROP COLUMN "+c)
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// Migration 21 turns existing productivity tasks into tickets without
// touching their data: each gets a unique sequence and version 1.
func TestTicketMigrationKeepsExistingTasks(t *testing.T) {
	s, path := openPipelineStore(t)
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"one", "two"} {
		tk := &model.Task{Title: title, Priority: 60, Day: "2026-11-22", Status: model.TaskPartial}
		if err := s.CreateTask(ctx, tk); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	undoTicketMigration(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE meta SET value='20' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seen := map[int]bool{}
	for _, id := range ids {
		tk, err := s.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.TicketSeq == 0 || seen[tk.TicketSeq] || tk.Version != 1 || tk.Status != model.TaskPartial || tk.Priority != 60 || tk.Day != "2026-11-22" {
			t.Fatalf("migrated task: %+v", tk)
		}
		seen[tk.TicketSeq] = true
	}
	fresh := &model.Task{Title: "three"}
	if err := s.CreateTask(ctx, fresh); err != nil || seen[fresh.TicketSeq] || fresh.TicketSeq == 0 {
		t.Fatalf("new task seq %d after migration: %v", fresh.TicketSeq, err)
	}
}
