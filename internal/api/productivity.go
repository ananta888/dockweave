package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
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
	_ = s.Store.SaveObjectiveRevision(r.Context(), &o)
	s.audit(r, model.EventObjectiveCreated, o.Title)
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
	if o, err := s.Store.GetObjective(r.Context(), r.PathValue("id")); err == nil {
		_ = s.Store.SaveObjectiveRevision(r.Context(), o)
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
	s.audit(r, model.EventTaskCreated, t.Title)
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
	s.audit(r, model.EventTaskUpdated, existing.Title)
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
	s.audit(r, model.EventFocusStarted, "focus "+f.TaskID)
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
	s.audit(r, model.EventBudgetUpdated, fmt.Sprintf("%s/%s", b.Scope, b.Kind))
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
	s.audit(r, model.EventReviewCreated, string(rv.Kind))
	writeJSON(w, http.StatusCreated, rv)
}

// batchWorkEvents ingests monitor/provider measurements idempotently.
func (s *Server) batchWorkEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Events []model.WorkEvent `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	inserted, duplicates := 0, 0
	for i := range req.Events {
		ok, err := s.Store.AddWorkEvent(r.Context(), &req.Events[i])
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err)
			return
		}
		if ok {
			inserted++
		} else {
			duplicates++
		}
	}
	writeJSON(w, http.StatusCreated, map[string]int{"inserted": inserted, "duplicates": duplicates})
}

// derivedWorkEvents returns container runtime computed from the audit log.
func (s *Server) derivedWorkEvents(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-7 * 24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	list, err := s.Store.AgentRuntimeIntervals(r.Context(), since)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "derived:audit", "since": since, "events": list,
		"note": "container runtime only; agent and LLM time need an external source",
	})
}

func budgetPeriodStart(period string, now time.Time) time.Time {
	switch period {
	case "weekly":
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(wd - 1))
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	default:
		return time.Time{}
	}
}

// budgetActuals compares targets with measured work events.
func (s *Server) budgetActuals(w http.ResponseWriter, r *http.Request) {
	budgets, err := s.Store.ListBudgets(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	events, err := s.Store.ListWorkEvents(r.Context(), "", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	verified, _ := s.Store.ListTasks(r.Context(), "", string(model.TaskVerified), "", 0)

	type actual struct {
		ID       string  `json:"id"`
		Scope    string  `json:"scope"`
		ScopeID  string  `json:"scope_id,omitempty"`
		Kind     string  `json:"kind"`
		Period   string  `json:"period"`
		Target   float64 `json:"target"`
		Actual   float64 `json:"actual"`
		Unit     string  `json:"unit"`
		Exceeded bool    `json:"exceeded"`
	}
	out := make([]actual, 0, len(budgets))
	for _, b := range budgets {
		start := budgetPeriodStart(b.Period, time.Now())
		var sum float64
		for _, e := range events {
			if !start.IsZero() && e.StartedAt.Before(start) {
				continue
			}
			switch b.Kind {
			case model.BudgetHumanHours:
				if e.Kind == model.EventHumanFocus {
					sum += float64(e.DurationSeconds) / 3600
				}
			case model.BudgetAgentHours:
				if e.Kind == model.EventAgentRun {
					sum += float64(e.DurationSeconds) / 3600
				}
			case model.BudgetTokens:
				if e.Kind == model.EventLLM {
					if v, err := strconv.ParseFloat(e.Meta["tokens"], 64); err == nil {
						sum += v
					}
				}
			case model.BudgetCostUSD:
				if v, err := strconv.ParseFloat(e.Meta["cost_usd"], 64); err == nil {
					sum += v
				}
			}
		}
		out = append(out, actual{
			ID: b.ID, Scope: b.Scope, ScopeID: b.ScopeID, Kind: string(b.Kind),
			Period: b.Period, Target: b.Target, Actual: sum, Unit: string(b.Kind),
			Exceeded: b.Target > 0 && sum > b.Target,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"budgets": out, "verified_results": len(verified),
		"note": "hours and tokens are inputs, not progress",
	})
}

// suggestTasks ranks the queue deterministically and explains each decision.
func (s *Server) suggestTasks(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if day == "" {
		day = today()
	}
	tasks, err := s.Store.ListTasks(r.Context(), day, "", string(model.AssigneeHuman), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	byID := map[string]*model.Task{}
	all, _ := s.Store.ListTasks(r.Context(), "", "", "", 0)
	for _, t := range all {
		byID[t.ID] = t
	}

	type suggestion struct {
		TaskID  string   `json:"task_id"`
		Title   string   `json:"title"`
		Score   float64  `json:"score"`
		Reasons []string `json:"reasons"`
		Blocked bool     `json:"blocked"`
	}
	out := make([]suggestion, 0, len(tasks))
	for _, t := range tasks {
		if t.IsFinished() {
			continue
		}
		score := float64(t.Priority) * 0.5
		reasons := []string{fmt.Sprintf("priority %d", t.Priority)}
		if len(t.ObjectiveIDs) > 0 {
			score += 10
			reasons = append(reasons, "contributes to an objective")
		}
		if t.EffortMinutes > 0 && t.EffortMinutes <= 30 {
			score += 5
			reasons = append(reasons, "small scope")
		}
		blocked := false
		for _, id := range t.BlockedBy {
			if b, ok := byID[id]; ok && !b.IsFinished() {
				blocked = true
				break
			}
		}
		if blocked {
			score -= 100
			reasons = append(reasons, "blocked by an open task")
		}
		out = append(out, suggestion{TaskID: t.ID, Title: t.Title, Score: score, Reasons: reasons, Blocked: blocked})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "suggestions": out})
}

// calendarLog returns work events grouped by day (logbook, not a plan).
func (s *Server) calendarLog(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListWorkEvents(r.Context(), "", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	start, end := r.URL.Query().Get("start"), r.URL.Query().Get("end")
	agent, human := r.URL.Query().Get("agent"), r.URL.Query().Get("human")
	days := map[string][]*model.WorkEvent{}
	for _, e := range events {
		day := e.StartedAt.Local().Format("2006-01-02")
		if start != "" && day < start {
			continue
		}
		if end != "" && day > end {
			continue
		}
		if agent != "" && e.AgentID != agent {
			continue
		}
		if human != "" && e.HumanID != human {
			continue
		}
		days[day] = append(days[day], e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "source": "work_events"})
}

// reviewReport builds the deterministic weekly/monthly reflection.
func (s *Server) reviewReport(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "weekly"
	}
	now := time.Now()
	var start time.Time
	if kind == "monthly" {
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	} else {
		start = budgetPeriodStart("weekly", now)
	}
	if v := r.URL.Query().Get("start"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			start = t
		}
	}
	tasks, _ := s.Store.ListTasks(r.Context(), "", "", "", 0)
	events, _ := s.Store.ListWorkEvents(r.Context(), "", 0)

	var done, verified, blocked, failed, retried int
	perObjective := map[string]float64{}
	focusSeconds, agentSeconds := int64(0), int64(0)
	cost := 0.0
	byObjective := map[string]string{}
	for _, t := range tasks {
		for _, o := range t.ObjectiveIDs {
			byObjective[t.ID] = o
		}
		if t.UpdatedAt.Before(start) {
			continue
		}
		switch t.Status {
		case model.TaskDone:
			done++
		case model.TaskVerified:
			verified++
		case model.TaskBlocked:
			blocked++
		case model.TaskFailed:
			failed++
		}
		if t.Attempts > 1 {
			retried++
		}
	}
	for _, e := range events {
		if e.StartedAt.Before(start) {
			continue
		}
		if e.Kind == model.EventHumanFocus {
			focusSeconds += e.DurationSeconds
		}
		if e.Kind == model.EventAgentRun {
			agentSeconds += e.DurationSeconds
		}
		if v, err := strconv.ParseFloat(e.Meta["cost_usd"], 64); err == nil {
			cost += v
		}
		if o := byObjective[e.TaskID]; o != "" {
			perObjective[o] += float64(e.DurationSeconds) / 3600
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": kind, "period_start": start.Format("2006-01-02"),
		"period_end": now.Format("2006-01-02"),
		"tasks_done": done, "tasks_verified": verified,
		"tasks_blocked": blocked, "tasks_failed": failed,
		"tasks_with_retries": retried,
		"human_hours":        float64(focusSeconds) / 3600,
		"agent_hours":        float64(agentSeconds) / 3600,
		"cost_usd":           cost, "hours_per_objective": perObjective,
		"generated_by": "deterministic", "llm_summary": nil,
	})
}

// exportProductivity returns the versioned, platform-neutral contract bundle.
// Export is explicit and read-only: it never creates grants or execution
// rights.
func (s *Server) exportProductivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	objectives, err := s.Store.ListObjectives(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	tasks, err := s.Store.ListTasks(ctx, "", "", "", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	events, err := s.Store.ListWorkEvents(ctx, "", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	budgets, err := s.Store.ListBudgets(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	reviews, err := s.Store.ListReviews(ctx, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	var evidence []*model.Evidence
	for _, t := range tasks {
		if list, err := s.Store.ListEvidence(ctx, t.ID); err == nil {
			evidence = append(evidence, list...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":  "https://dockweave.local/schema/productivity/v1.json",
		"version": 1, "exported_at": time.Now().UTC(),
		"objectives": objectives, "tasks": tasks, "work_events": events,
		"evidence": evidence, "budgets": budgets, "reviews": reviews,
		"note": "read-only export; grants no execution rights",
	})
}

// audit writes productivity changes into the hash-chained audit log.
func (s *Server) audit(r *http.Request, typ model.EventType, msg string) {
	_ = s.Store.AppendEvent(r.Context(), &model.Event{
		ID: model.NewID("evt"), Type: typ, Actor: "api", Message: msg,
		Timestamp: time.Now().UTC(),
	})
}

// rolloverTasks moves unfinished human tasks to another day.
func (s *Server) rolloverTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.To == "" {
		req.To = today()
	}
	if req.From == "" {
		req.From = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	n, err := s.Store.RolloverTasks(r.Context(), req.From, req.To)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, model.EventTaskUpdated, fmt.Sprintf("rollover %s -> %s: %d tasks", req.From, req.To, n))
	writeJSON(w, http.StatusOK, map[string]any{"from": req.From, "to": req.To, "moved": n})
}

// switchFocus ends the current session (skip) and starts the given task.
func (s *Server) switchFocus(w http.ResponseWriter, r *http.Request) {
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
	if _, err := s.Store.EndFocus(r.Context(), req.Human, false, "switched"); err != nil && !errors.Is(err, persistenceErrNotFound()) {
		s.fail(w, err)
		return
	}
	f, err := s.Store.StartFocus(r.Context(), req.TaskID, req.Human)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, model.EventFocusStarted, "switch to "+req.TaskID)
	writeJSON(w, http.StatusCreated, f)
}

// pruneProductivity deletes old measurements and reviews (retention policy).
func (s *Server) pruneProductivity(w http.ResponseWriter, r *http.Request) {
	before := time.Now().AddDate(0, 0, -90)
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			before = t
		}
	}
	counts, err := s.Store.PruneProductivity(r.Context(), before)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, model.EventProductivityPrune, fmt.Sprintf("pruned before %s", before.Format("2006-01-02")))
	writeJSON(w, http.StatusOK, map[string]any{"before": before.Format("2006-01-02"), "deleted": counts})
}

func persistenceErrNotFound() error { return persistence.ErrNotFound }

// listObjectiveRevisions returns the version history of an objective.
func (s *Server) listObjectiveRevisions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ObjectiveRevisions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
