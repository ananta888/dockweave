package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Productivity API: goals, daily tasks, focus, measurements, evidence,
// budgets and reviews. Deterministic by design; no LLM calls here.

func today() string { return time.Now().Format("2006-01-02") }

func humanFrom(r *http.Request) string {
	if h := strings.TrimSpace(r.URL.Query().Get("human")); h != "" {
		return h
	}
	return "local"
}

func (s *Server) listObjectives(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListObjectives(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createObjective(w http.ResponseWriter, r *http.Request) {
	var o model.Objective
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.CreateObjective(r.Context(), &o); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func (s *Server) updateObjective(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.UpdateObjectiveStatus(r.Context(), r.PathValue("id"), req.Status); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.Store.ListTasks(r.Context(), q.Get("day"), q.Get("status"), q.Get("assignee"), 500)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var t model.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.CreateTask(r.Context(), &t); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	existing, err := s.Store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(existing); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.UpdateTask(r.Context(), existing); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteTask(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) todayPlan(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if day == "" {
		day = today()
	}
	plan, err := s.Store.TodayPlan(r.Context(), day, humanFrom(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) startFocus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID string `json:"task_id"`
		Human  string `json:"human"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if req.Human == "" {
		req.Human = humanFrom(r)
	}
	f, err := s.Store.StartFocus(r.Context(), req.TaskID, req.Human)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) currentFocus(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.CurrentFocus(r.Context(), humanFrom(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) pauseFocus(w http.ResponseWriter, r *http.Request) {
	s.setFocus(w, r, model.FocusPaused)
}

func (s *Server) resumeFocus(w http.ResponseWriter, r *http.Request) {
	s.setFocus(w, r, model.FocusActive)
}

func (s *Server) setFocus(w http.ResponseWriter, r *http.Request, status model.FocusStatus) {
	f, err := s.Store.SetFocusStatus(r.Context(), humanFrom(r), string(status))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) endFocus(w http.ResponseWriter, r *http.Request, complete bool) {
	var req struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f, err := s.Store.EndFocus(r.Context(), humanFrom(r), complete, req.Notes)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) completeFocus(w http.ResponseWriter, r *http.Request) { s.endFocus(w, r, true) }
func (s *Server) skipFocus(w http.ResponseWriter, r *http.Request)     { s.endFocus(w, r, false) }

func (s *Server) createWorkEvent(w http.ResponseWriter, r *http.Request) {
	var e model.WorkEvent
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	inserted, err := s.Store.AddWorkEvent(r.Context(), &e)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusCreated
	if !inserted {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"inserted": inserted, "event": e})
}

func (s *Server) listWorkEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListWorkEvents(r.Context(), r.URL.Query().Get("task"), 500)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createEvidence(w http.ResponseWriter, r *http.Request) {
	var e model.Evidence
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	inserted, err := s.Store.AddEvidence(r.Context(), &e)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusCreated
	if !inserted {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"inserted": inserted, "evidence": e})
}

func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) listBudgets(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListBudgets(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createBudget(w http.ResponseWriter, r *http.Request) {
	var b model.InputBudget
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.UpsertBudget(r.Context(), &b); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListReviews(r.Context(), 50)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	var rv model.Review
	if err := json.NewDecoder(r.Body).Decode(&rv); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := s.Store.CreateReview(r.Context(), &rv); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rv)
}
