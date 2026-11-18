package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
