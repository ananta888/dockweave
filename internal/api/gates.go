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
