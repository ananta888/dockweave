package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	st, err := persistence.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{Store: st}
}

func call(t *testing.T, s *Server, h http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(ContextWithPeer(context.Background(), "test", 1000, RoleAdmin))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestAPITaskFocusEvidenceFlow(t *testing.T) {
	s := testServer(t)

	rec := call(t, s, s.createObjective, http.MethodPost, "/api/v1/objectives",
		model.Objective{Title: "Ship productivity MVP", Kind: model.ObjectiveQuarter})
	if rec.Code != http.StatusCreated {
		t.Fatalf("objective status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, s, s.createTask, http.MethodPost, "/api/v1/tasks",
		model.Task{Title: "Wire the CLI", Day: "2026-11-18", Priority: 70})
	if rec.Code != http.StatusCreated {
		t.Fatalf("task status = %d: %s", rec.Code, rec.Body.String())
	}
	var task model.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &task)

	rec = call(t, s, s.startFocus, http.MethodPost, "/api/v1/focus/start",
		map[string]string{"task_id": task.ID, "human": "mira"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("focus start = %d: %s", rec.Code, rec.Body.String())
	}

	// A second session for the same human is a conflict (HTTP 409).
	rec = call(t, s, s.startFocus, http.MethodPost, "/api/v1/focus/start",
		map[string]string{"task_id": task.ID, "human": "mira"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second focus = %d, want 409", rec.Code)
	}

	rec = call(t, s, s.completeFocus, http.MethodPost, "/api/v1/focus/complete?human=mira",
		map[string]string{"notes": "done"})
	if rec.Code != http.StatusOK {
		t.Fatalf("focus complete = %d: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, s, s.createEvidence, http.MethodPost, "/api/v1/evidence",
		model.Evidence{TaskID: task.ID, Kind: model.EvidenceTest,
			Status: model.EvidencePassed, Verified: true, IdempotencyKey: "test:1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("evidence = %d: %s", rec.Code, rec.Body.String())
	}

	// Same idempotency key is accepted but not inserted twice.
	rec = call(t, s, s.createEvidence, http.MethodPost, "/api/v1/evidence",
		model.Evidence{TaskID: task.ID, Kind: model.EvidenceTest,
			Status: model.EvidencePassed, Verified: true, IdempotencyKey: "test:1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate evidence = %d", rec.Code)
	}

	got, err := s.Store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskVerified {
		t.Fatalf("task status = %s, want verified", got.Status)
	}
}

func TestAPITodayPlanAndBudget(t *testing.T) {
	s := testServer(t)
	_ = s.Store.CreateTask(context.Background(), &model.Task{Title: "a", Day: "2026-11-18"})

	rec := call(t, s, s.todayPlan, http.MethodGet, "/api/v1/tasks/today?day=2026-11-18&human=mira", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan = %d: %s", rec.Code, rec.Body.String())
	}
	var plan model.TodayPlan
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	if len(plan.Tasks) != 1 {
		t.Fatalf("plan tasks = %d", len(plan.Tasks))
	}

	rec = call(t, s, s.createBudget, http.MethodPost, "/api/v1/budgets",
		model.InputBudget{Scope: "global", Kind: model.BudgetHumanHours, Period: "weekly", Target: 10})
	if rec.Code != http.StatusCreated {
		t.Fatalf("budget = %d: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, s, s.listBudgets, http.MethodGet, "/api/v1/budgets", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list budgets = %d", rec.Code)
	}
}

func TestAPISecretKeysAreRejected(t *testing.T) {
	s := testServer(t)
	task := &model.Task{Title: "x", Day: "2026-11-18"}
	_ = s.Store.CreateTask(context.Background(), task)

	rec := call(t, s, s.createWorkEvent, http.MethodPost, "/api/v1/work-events",
		model.WorkEvent{
			Kind: model.EventLLM, StartedAt: timeNow(), DurationSeconds: 3,
			Meta: map[string]string{"api_key": "sk-should-not-be-stored"},
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("event with secret = %d, want 400", rec.Code)
	}

	rec = call(t, s, s.createEvidence, http.MethodPost, "/api/v1/evidence",
		model.Evidence{
			TaskID: task.ID, Kind: model.EvidenceTest, Status: model.EvidencePassed,
			Payload: map[string]string{"token": "dws_leak"},
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("evidence with secret = %d, want 400", rec.Code)
	}
}

func TestDerivedSuggestCalendarAndReport(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	task := &model.Task{Title: "rank me", Day: "2026-11-18", Priority: 80, EffortMinutes: 20}
	_ = s.Store.CreateTask(ctx, task)
	_, _ = s.Store.AddWorkEvent(ctx, &model.WorkEvent{
		Kind: model.EventHumanFocus, StartedAt: timeNow(), DurationSeconds: 600,
		IdempotencyKey: "focus:test",
	})

	for name, h := range map[string]http.HandlerFunc{
		"derived":  s.derivedWorkEvents,
		"suggest":  s.suggestTasks,
		"calendar": s.calendarLog,
		"report":   s.reviewReport,
		"export":   s.exportProductivity,
	} {
		method := http.MethodGet
		rec := call(t, s, h, method, "/api/v1/"+name, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	rec := call(t, s, s.suggestTasks, http.MethodGet, "/api/v1/tasks/suggest?day=2026-11-18", nil)
	var out struct {
		Suggestions []struct {
			TaskID  string   `json:"task_id"`
			Reasons []string `json:"reasons"`
		} `json:"suggestions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Suggestions) != 1 || len(out.Suggestions[0].Reasons) == 0 {
		t.Fatalf("suggestions = %+v", out.Suggestions)
	}
}

func timeNow() time.Time { return time.Now().UTC() }

func TestRolloverSwitchAndAudit(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	yesterday := "2026-11-17"
	a := &model.Task{Title: "a", Day: yesterday}
	b := &model.Task{Title: "b", Day: "2026-11-18"}
	_ = s.Store.CreateTask(ctx, a)
	_ = s.Store.CreateTask(ctx, b)

	// Focus a, then switch to b: a goes back to the queue.
	if _, err := s.Store.StartFocus(ctx, a.ID, "mira"); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, s.switchFocus, http.MethodPost, "/api/v1/focus/switch",
		map[string]string{"task_id": b.ID, "human": "mira"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("switch = %d: %s", rec.Code, rec.Body.String())
	}
	gotA, _ := s.Store.GetTask(ctx, a.ID)
	if gotA.Status != model.TaskTodo {
		t.Fatalf("switched-away task = %s, want todo", gotA.Status)
	}

	rec = call(t, s, s.rolloverTasks, http.MethodPost, "/api/v1/tasks/rollover",
		map[string]string{"from": yesterday, "to": "2026-11-18"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rollover = %d: %s", rec.Code, rec.Body.String())
	}
	plan, _ := s.Store.TodayPlan(ctx, "2026-11-18", "mira")
	if len(plan.Tasks) != 2 {
		t.Fatalf("today = %d tasks, want 2", len(plan.Tasks))
	}

	events, _ := s.Store.ListEvents(ctx, 50)
	found := false
	for _, e := range events {
		if e.Type == model.EventTaskUpdated || e.Type == model.EventFocusStarted {
			found = true
		}
	}
	if !found {
		t.Fatal("expected productivity audit events")
	}
}

func TestPruneEndpoint(t *testing.T) {
	s := testServer(t)
	rec := call(t, s, s.pruneProductivity, http.MethodPost,
		"/api/v1/productivity/prune?before=2026-01-01", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("prune = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestObjectiveRevisionsAndAttempts(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()

	rec := call(t, s, s.createObjective, http.MethodPost, "/api/v1/objectives",
		model.Objective{Title: "v1", Kind: model.ObjectiveQuarter})
	var obj model.Objective
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/objectives/"+obj.ID,
		bytes.NewBufferString(`{"status":"achieved"}`))
	req.SetPathValue("id", obj.ID)
	req = req.WithContext(ContextWithPeer(context.Background(), "test", 1000, RoleAdmin))
	rec = httptest.NewRecorder()
	s.updateObjective(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/objectives/"+obj.ID+"/revisions", nil)
	req.SetPathValue("id", obj.ID)
	req = req.WithContext(ContextWithPeer(context.Background(), "test", 1000, RoleAdmin))
	rec = httptest.NewRecorder()
	s.listObjectiveRevisions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revisions = %d", rec.Code)
	}
	var revisions []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &revisions)
	if len(revisions) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revisions))
	}

	task := &model.Task{Title: "retry me", Day: "2026-11-18"}
	_ = s.Store.CreateTask(ctx, task)
	if _, err := s.Store.StartFocus(ctx, task.ID, "mira"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Store.EndFocus(ctx, "mira", false, "skip")
	if _, err := s.Store.StartFocus(ctx, task.ID, "mira"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Store.GetTask(ctx, task.ID)
	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
}
