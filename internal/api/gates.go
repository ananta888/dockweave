package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

func (s *Server) runGate(w http.ResponseWriter, r *http.Request) {
	if s.Gates == nil {
		writeErr(w, http.StatusNotImplemented, "unavailable", errors.New("gate runner is not configured"))
		return
	}
	var body struct {
		Workspace  string `json:"workspace"`
		IssueID    string `json:"issue_id"`
		Image      string `json:"image"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode gate request: %w", err))
		return
	}
	timeout := time.Duration(body.TimeoutSec) * time.Second
	run, err := s.Gates.Run(r.Context(), gateexec.Request{
		Workspace: body.Workspace,
		IssueID:   body.IssueID,
		Image:     body.Image,
		Actor:     actor(r),
		Timeout:   timeout,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) listGateRuns(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Store.ListGateRuns(r.Context(), workspace, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) createGateApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID string `json:"issue_id"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode approval: %w", err))
		return
	}
	body.IssueID = strings.TrimSpace(body.IssueID)
	if body.IssueID == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("issue_id is required"))
		return
	}
	role, _ := r.Context().Value(peerRoleKey{}).(Role)
	if role != RoleAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("only admins may approve"))
		return
	}
	approval := &model.GateApproval{
		IssueID:    body.IssueID,
		Actor:      actor(r),
		Role:       string(role),
		Note:       strings.TrimSpace(body.Note),
		ApprovedAt: time.Now().UTC(),
	}
	if err := s.Store.SaveGateApproval(r.Context(), approval); err != nil {
		s.fail(w, err)
		return
	}
	_ = s.Store.AppendEvent(r.Context(), &model.Event{
		ID:        fmt.Sprintf("evt-approval-%d", time.Now().UnixNano()),
		Type:      model.EventGateApproval,
		Actor:     approval.Actor,
		Message:   fmt.Sprintf("issue %s approved by %s (%s)", approval.IssueID, approval.Actor, approval.Role),
		Timestamp: approval.ApprovedAt,
	})
	writeJSON(w, http.StatusOK, approval)
}

func (s *Server) getGateApproval(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(r.PathValue("issue"))
	approval, err := s.Store.GetGateApproval(r.Context(), issueID)
	if errors.Is(err, persistence.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approval)
}

func (s *Server) createGateReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID          string `json:"issue_id"`
		Verdict          string `json:"verdict"`
		Note             string `json:"note"`
		AssignedReviewer string `json:"assigned_reviewer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode review: %w", err))
		return
	}
	body.IssueID = strings.TrimSpace(body.IssueID)
	body.Verdict = strings.ToUpper(strings.TrimSpace(body.Verdict))
	if body.IssueID == "" || (body.Verdict != "PASS" && body.Verdict != "FAIL") {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("issue_id and verdict PASS|FAIL are required"))
		return
	}

	// Attribution: a review verdict is only trustworthy when it comes from the
	// issue's assigned reviewer or from an admin. The Paperclip wrapper verifies
	// assignment before calling here and passes the assignee; the daemon
	// re-checks it so a bare peer cannot record an unattributed verdict.
	role, _ := r.Context().Value(peerRoleKey{}).(Role)
	reviewer := strings.TrimSpace(body.AssignedReviewer)
	isAdmin := role == RoleAdmin
	if reviewer == "" && !isAdmin {
		writeErr(w, http.StatusForbidden, "forbidden",
			errors.New("reviews require an assigned reviewer or admin role"))
		return
	}
	if reviewer != "" && !isAdmin && actor(r) != reviewer {
		writeErr(w, http.StatusForbidden, "forbidden",
			fmt.Errorf("review for issue %s is assigned to %s, not %s", body.IssueID, reviewer, actor(r)))
		return
	}

	review := &model.GateReview{
		IssueID:          body.IssueID,
		Verdict:          body.Verdict,
		Actor:            actor(r),
		Note:             strings.TrimSpace(body.Note),
		AssignedReviewer: reviewer,
	}
	saved, err := s.Store.UpsertGateReview(r.Context(), review)
	if errors.Is(err, persistence.ErrConflict) {
		_ = s.Store.AppendEvent(r.Context(), &model.Event{
			ID:        fmt.Sprintf("evt-review-%d", time.Now().UnixNano()),
			Type:      model.EventGateReview,
			Actor:     saved.Actor,
			Message:   fmt.Sprintf("issue %s review conflict: incoming %s, stored %s", saved.IssueID, body.Verdict, saved.Existing),
			Timestamp: time.Now().UTC(),
		})
		writeJSON(w, http.StatusConflict, saved)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	_ = s.Store.AppendEvent(r.Context(), &model.Event{
		ID:        fmt.Sprintf("evt-review-%d", time.Now().UnixNano()),
		Type:      model.EventGateReview,
		Actor:     saved.Actor,
		Message:   fmt.Sprintf("issue %s review %s (evidence %s)", saved.IssueID, saved.Verdict, saved.EvidenceID),
		Timestamp: saved.RecordedAt,
	})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) getGateReview(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(r.PathValue("issue"))
	review, err := s.Store.GetGateReview(r.Context(), issueID)
	if errors.Is(err, persistence.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (s *Server) listGateReviews(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	reviews, err := s.Store.ListGateReviews(r.Context(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": reviews})
}

func (s *Server) pruneGateRuns(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(peerRoleKey{}).(Role)
	if role != RoleAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("only admins may prune gate runs"))
		return
	}
	var body struct {
		Days             int `json:"days"`
		KeepPerWorkspace int `json:"keep_per_workspace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode prune: %w", err))
		return
	}
	if body.Days <= 0 {
		body.Days = 30
	}
	if body.KeepPerWorkspace <= 0 {
		body.KeepPerWorkspace = 20
	}
	n, err := s.Store.PruneGateRuns(r.Context(),
		time.Now().Add(-time.Duration(body.Days)*24*time.Hour), body.KeepPerWorkspace)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pruned": n})
}
