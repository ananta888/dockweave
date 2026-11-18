package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestFocusSessionLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	task := &model.Task{Title: "Write the plan", Day: "2026-11-18"}
	if err := st.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	f, err := st.StartFocus(ctx, task.ID, "mira")
	if err != nil {
		t.Fatalf("start focus: %v", err)
	}
	if f.Status != model.FocusActive {
		t.Fatalf("status = %s", f.Status)
	}

	// A human may only have one active session.
	if _, err := st.StartFocus(ctx, task.ID, "mira"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second session error = %v, want conflict", err)
	}

	if _, err := st.SetFocusStatus(ctx, "mira", string(model.FocusPaused)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := st.SetFocusStatus(ctx, "mira", string(model.FocusActive)); err != nil {
		t.Fatalf("resume: %v", err)
	}

	done, err := st.EndFocus(ctx, "mira", true, "finished")
	if err != nil {
		t.Fatalf("end focus: %v", err)
	}
	if done.Status != model.FocusCompleted {
		t.Fatalf("end status = %s", done.Status)
	}

	got, err := st.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskDone {
		t.Fatalf("task status = %s, want done", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed_at not set")
	}

	events, err := st.ListWorkEvents(ctx, task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != model.EventHumanFocus {
		t.Fatalf("events = %+v", events)
	}
}

func TestWorkEventIdempotency(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	e := &model.WorkEvent{
		Kind: model.EventAgentRun, AgentID: "ag_1", StartedAt: now,
		DurationSeconds: 42, IdempotencyKey: "run:42",
	}
	inserted, err := st.AddWorkEvent(ctx, e)
	if err != nil || !inserted {
		t.Fatalf("first insert = %v/%v", inserted, err)
	}
	again := *e
	again.ID = ""
	inserted, err = st.AddWorkEvent(ctx, &again)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate key was inserted twice")
	}
	events, err := st.ListWorkEvents(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestEvidencePromotesDoneToVerified(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	task := &model.Task{Title: "Ship it", Status: model.TaskDone}
	if err := st.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEvidence(ctx, &model.Evidence{
		TaskID: task.ID, Kind: model.EvidenceTest, Status: model.EvidencePassed,
		Ref: "go test ./...", Verified: true, IdempotencyKey: "ci:1",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskVerified {
		t.Fatalf("status = %s, want verified", got.Status)
	}

	// A failing or unverified proof must not promote a task.
	task2 := &model.Task{Title: "Unverified", Status: model.TaskDone}
	_ = st.CreateTask(ctx, task2)
	_, _ = st.AddEvidence(ctx, &model.Evidence{
		TaskID: task2.ID, Kind: model.EvidenceBuild, Status: model.EvidencePassed,
		Verified: false, IdempotencyKey: "ci:2",
	})
	got2, _ := st.GetTask(ctx, task2.ID)
	if got2.Status != model.TaskDone {
		t.Fatalf("unverified task status = %s", got2.Status)
	}
}

func TestTodayPlanOrdersAndReportsFocus(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	day := "2026-11-18"
	for i, title := range []string{"second", "first"} {
		_ = st.CreateTask(ctx, &model.Task{Title: title, Day: day, OrderIndex: i, Priority: 10})
	}
	_ = st.CreateTask(ctx, &model.Task{Title: "other day", Day: "2026-11-19"})

	plan, err := st.TodayPlan(ctx, day, "mira")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(plan.Tasks))
	}
	if plan.Tasks[0].Title != "second" || plan.Tasks[1].Title != "first" {
		t.Fatalf("order = %s, %s", plan.Tasks[0].Title, plan.Tasks[1].Title)
	}
}

func TestBulkDeleteTaskRemovesFocus(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	task := &model.Task{Title: "temp"}
	_ = st.CreateTask(ctx, task)
	if _, err := st.StartFocus(ctx, task.ID, "mira"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CurrentFocus(ctx, "mira"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focus still present: %v", err)
	}
}

func TestRolloverOnlyUnfinishedHumanTasks(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_ = st.CreateTask(ctx, &model.Task{Title: "open", Day: "2026-11-17"})
	_ = st.CreateTask(ctx, &model.Task{Title: "done", Day: "2026-11-17", Status: model.TaskDone})
	_ = st.CreateTask(ctx, &model.Task{Title: "agent", Day: "2026-11-17", AssigneeKind: model.AssigneeAgent, AgentID: "ag_1"})

	n, err := st.RolloverTasks(ctx, "2026-11-17", "2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("moved = %d, want 1", n)
	}
	open, _ := st.ListTasks(ctx, "2026-11-18", "", "", 0)
	if len(open) != 1 || open[0].Title != "open" {
		t.Fatalf("today = %+v", open)
	}
}

func TestPruneRemovesOldMeasurements(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -120)
	_, _ = st.AddWorkEvent(ctx, &model.WorkEvent{
		Kind: model.EventHumanFocus, StartedAt: old, DurationSeconds: 60,
		IdempotencyKey: "old",
	})
	_, _ = st.AddWorkEvent(ctx, &model.WorkEvent{
		Kind: model.EventHumanFocus, StartedAt: time.Now(), DurationSeconds: 60,
		IdempotencyKey: "new",
	})
	counts, err := st.PruneProductivity(ctx, time.Now().AddDate(0, 0, -90))
	if err != nil {
		t.Fatal(err)
	}
	if counts["work_events"] != 1 {
		t.Fatalf("deleted = %v", counts)
	}
	events, _ := st.ListWorkEvents(ctx, "", 0)
	if len(events) != 1 || events[0].IdempotencyKey != "new" {
		t.Fatalf("events = %+v", events)
	}
}

func TestMigrationSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task := &model.Task{Title: "persist me"}
	if err := st.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	got, err := st2.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("read after reopen: %v", err)
	}
	if got.Title != "persist me" {
		t.Fatalf("title = %s", got.Title)
	}
}
