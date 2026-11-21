package api

// Pipeline edges (gate -> agent) and their enforcement, see ADR 0002.
//
//	PUT  /api/v1/pipelines             admin    replace edges for a graph revision
//	GET  /api/v1/pipelines             read     stored edges + revision
//	GET  /api/v1/pipelines/admission   read     may <agent> take pipeline work?
//	GET  /api/v1/agents/{id}/pipeline  read     same, as agent status
//	POST /api/v1/pipelines/override    admin    audited exception for one edge
//	GET  /api/v1/pipelines/overrides   read     list overrides
//
// Enforcement happens where Paperclip actually drives an agent: the
// OpenAI-compatible POST /v1/chat/completions (and the herdr prompt). A
// blocked agent gets 409 pipeline_blocked before any model turn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/pipeline"
)

const (
	maxPipelineEdges     = 1000
	defaultOverrideTTL   = 15 * time.Minute
	maxOverrideTTL       = 24 * time.Hour
	blockedAuditInterval = time.Minute
	issueHeader          = "X-DockWeave-Issue"
)

var gateIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// pipelineEvaluator returns the configured evaluator or a default one backed
// by the store and the gate fingerprint (with the runner's limits).
func (s *Server) pipelineEvaluator() *pipeline.Evaluator {
	if s.Pipeline != nil {
		return s.Pipeline
	}
	var lim gateexec.FingerprintLimits
	if s.Gates != nil {
		lim = s.Gates.FingerprintLimits
	}
	ev := &pipeline.Evaluator{
		Fingerprint: func(ctx context.Context, ws string) (string, error) {
			return gateexec.Fingerprint(ctx, ws, lim)
		},
	}
	if s.Store != nil {
		ev.Store = s.Store
	}
	return ev
}

func isAdmin(r *http.Request) bool {
	role, _ := r.Context().Value(peerRoleKey{}).(Role)
	return role == RoleAdmin
}

type putPipelinesReq struct {
	Revision int                  `json:"revision"`
	Reset    bool                 `json:"reset"`
	Edges    []model.PipelineEdge `json:"edges"`
}

func (s *Server) putPipelines(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("only admins may change pipelines"))
		return
	}
	var body putPipelinesReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode pipelines: %w", err))
		return
	}
	if body.Revision < 0 {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("revision must not be negative"))
		return
	}
	if len(body.Edges) > maxPipelineEdges {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("at most %d edges are allowed", maxPipelineEdges))
		return
	}
	edges := make([]*model.PipelineEdge, 0, len(body.Edges))
	var problems []string
	for i := range body.Edges {
		e := body.Edges[i]
		if err := s.validateEdge(r.Context(), &e); err != nil {
			problems = append(problems, fmt.Sprintf("edge %d (%s -> %s): %v", i, e.GateID, e.DownstreamAgentID, err))
			continue
		}
		edges = append(edges, &e)
	}
	if len(problems) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New(strings.Join(problems, "; ")))
		return
	}
	err := s.Store.ReplacePipelineEdgesAudited(r.Context(), body.Revision, edges, body.Reset, &model.Event{
		ID: model.NewID("evt"), Type: model.EventPipelineUpdate, Actor: actor(r),
		Message: fmt.Sprintf("pipeline revision %d reset=%t: %d edges", body.Revision, body.Reset, len(edges)), Timestamp: time.Now().UTC(),
	})
	if errors.Is(err, persistence.ErrStaleRevision) {
		writeErr(w, http.StatusConflict, "stale_revision", err)
		return
	}
	if errors.Is(err, persistence.ErrRevisionConflict) {
		writeErr(w, http.StatusConflict, "revision_conflict", err)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	state, err := s.Store.ListPipelineEdges(r.Context(), persistence.PipelineEdgeFilter{})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// validateEdge normalises and checks one edge: gate id shape, existing
// agents (stored by id), canonical workspace below the allowed roots
// (AGENTS.md rule 4) and valid routing actions.
func (s *Server) validateEdge(ctx context.Context, e *model.PipelineEdge) error {
	e.GateID = strings.TrimSpace(e.GateID)
	if !gateIDRe.MatchString(e.GateID) {
		return fmt.Errorf("invalid gate_id %q", e.GateID)
	}
	down := strings.TrimSpace(e.DownstreamAgentID)
	if down == "" {
		return errors.New("downstream_agent_id is required")
	}
	a, err := s.Agents.Get(ctx, down)
	if err != nil {
		return fmt.Errorf("downstream agent %q: %w", down, err)
	}
	e.DownstreamAgentID = a.ID
	if up := strings.TrimSpace(e.UpstreamAgentID); up != "" {
		ua, err := s.Agents.Get(ctx, up)
		if err != nil {
			return fmt.Errorf("upstream agent %q: %w", up, err)
		}
		e.UpstreamAgentID = ua.ID
		if ua.ID == a.ID {
			return errors.New("upstream and downstream agent must differ")
		}
	}
	ws, err := s.canonicalGateWorkspace(e.Workspace)
	if err != nil {
		return err
	}
	e.Workspace = ws
	tAct, fAct, tReason, fReason := gatesync.ResolveRouting(e.OnTrue, e.OnFalse, e.ReasonTrue, e.ReasonFalse)
	for _, raw := range []string{e.OnTrue, e.OnFalse} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if _, ok := gatesync.ParseAction(raw); !ok {
			return fmt.Errorf("invalid action %q (want allow|deny|wake:<agent>|rework)", raw)
		}
	}
	e.OnTrue, e.OnFalse, e.ReasonTrue, e.ReasonFalse = tAct.String(), fAct.String(), tReason, fReason
	if e.MaxAgeSec < 0 || e.MaxAgeSec > model.MaxPipelineAgeSec {
		return fmt.Errorf("max_age_sec must be between 0 and %d", model.MaxPipelineAgeSec)
	}
	e.ID = strings.TrimSpace(e.ID)
	if e.ID == "" {
		e.ID = fmt.Sprintf("pe-%s-%s", e.GateID, e.DownstreamAgentID)
	}
	if !gateIDRe.MatchString(e.ID) {
		return fmt.Errorf("invalid edge id %q", e.ID)
	}
	return nil
}

// canonicalGateWorkspace resolves a workspace like the gate runner does.
func (s *Server) canonicalGateWorkspace(raw string) (string, error) {
	if s.Gates != nil {
		return s.Gates.ValidateWorkspace(raw)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", errors.New("workspace must be an absolute path")
	}
	canon, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(canon)
	if err != nil || !info.IsDir() {
		return "", errors.New("workspace is not a directory")
	}
	if len(s.AllowedRoots) > 0 && !underAnyRoot(canon, s.AllowedRoots) {
		return "", fmt.Errorf("workspace %q is outside the allowed roots", canon)
	}
	return canon, nil
}

func underAnyRoot(p string, roots []string) bool {
	for _, root := range roots {
		cr, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(cr, p)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}

func (s *Server) listPipelines(w http.ResponseWriter, r *http.Request) {
	f := persistence.PipelineEdgeFilter{}
	if q := strings.TrimSpace(r.URL.Query().Get("agent")); q != "" {
		a, err := s.Agents.Get(r.Context(), q)
		if err != nil {
			s.fail(w, err)
			return
		}
		f.DownstreamAgentID = a.ID
	}
	state, err := s.Store.ListPipelineEdges(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) pipelineAdmission(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("agent"))
	if q == "" {
		q = strings.TrimSpace(r.PathValue("id"))
	}
	if q == "" {
		// Without an agent: the admission of every agent that has edges
		// (used by the 2D graph status view).
		s.allAdmissions(w, r)
		return
	}
	a, err := s.Agents.Get(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	adm := s.pipelineEvaluator().Admission(r.Context(), a.ID)
	adm.Agent = a.Name
	writeJSON(w, http.StatusOK, adm)
}

func (s *Server) allAdmissions(w http.ResponseWriter, r *http.Request) {
	state, err := s.Store.ListPipelineEdges(r.Context(), persistence.PipelineEdgeFilter{})
	if err != nil {
		s.fail(w, err)
		return
	}
	ev := s.pipelineEvaluator()
	out := ev.Admissions(r.Context(), state)
	for _, adm := range out {
		if a, err := s.Agents.Get(r.Context(), adm.AgentID); err == nil {
			adm.Agent = a.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": state.Revision, "admissions": out})
}

func (s *Server) createPipelineOverride(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("only admins may override pipelines"))
		return
	}
	var body struct {
		Agent     string `json:"agent"`
		GateID    string `json:"gate_id"`
		Reason    string `json:"reason"`
		TTLSec    int    `json:"ttl_sec"`
		SingleUse *bool  `json:"single_use"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode override: %w", err))
		return
	}
	body.GateID = strings.TrimSpace(body.GateID)
	body.Reason = strings.TrimSpace(body.Reason)
	if body.GateID != "*" && !gateIDRe.MatchString(body.GateID) {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("gate_id is required (or * for every gate of the agent)"))
		return
	}
	if len(body.Reason) < 3 {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("a reason is required"))
		return
	}
	if body.TTLSec < 0 || body.TTLSec > int(maxOverrideTTL/time.Second) {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("ttl_sec must be between 0 and %d", int(maxOverrideTTL/time.Second)))
		return
	}
	ttl := time.Duration(body.TTLSec) * time.Second
	if ttl == 0 {
		ttl = defaultOverrideTTL
	}
	a, err := s.Agents.Get(r.Context(), strings.TrimSpace(body.Agent))
	if err != nil {
		s.fail(w, err)
		return
	}
	single := true
	if body.SingleUse != nil {
		single = *body.SingleUse
	}
	now := time.Now().UTC()
	o := &model.PipelineOverride{
		ID: fmt.Sprintf("po-%d", now.UnixNano()), AgentID: a.ID, GateID: body.GateID, Reason: body.Reason,
		Actor: actor(r), SingleUse: single, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if err := s.Store.CreatePipelineOverride(r.Context(), o); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) listPipelineOverrides(w http.ResponseWriter, r *http.Request) {
	agentID := ""
	if q := strings.TrimSpace(r.URL.Query().Get("agent")); q != "" {
		a, err := s.Agents.Get(r.Context(), q)
		if err != nil {
			s.fail(w, err)
			return
		}
		agentID = a.ID
	}
	var at time.Time
	if r.URL.Query().Get("active") == "true" {
		at = time.Now().UTC()
	}
	list, err := s.Store.ListPipelineOverrides(r.Context(), agentID, at)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []*model.PipelineOverride{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": list})
}

// admitPipelineWork enforces the pipeline admission before an agent is
// driven. It writes a 409 pipeline_blocked response and returns false when
// the agent may not work; nothing has been sent to the agent at that point.
func (s *Server) admitPipelineWork(w http.ResponseWriter, r *http.Request, a *model.Agent) bool {
	ev := *s.pipelineEvaluator()
	ev.WorkActor, ev.WorkIssue = actor(r), strings.TrimSpace(r.Header.Get(issueHeader))
	adm := ev.Admit(r.Context(), a.ID)
	adm.Agent = a.Name
	issue := strings.TrimSpace(r.Header.Get(issueHeader))
	if adm.Allowed {
		return true
	}
	s.auditBlocked(r, a, adm, issue)
	writePipelineBlocked(w, adm)
	return false
}

// writePipelineBlocked writes an OpenAI-compatible error object with the
// structured admission attached.
func writePipelineBlocked(w http.ResponseWriter, adm *model.Admission) {
	blocking := adm.Blocking()
	parts := make([]string, 0, len(blocking))
	gate := ""
	for _, e := range blocking {
		if gate == "" {
			gate = e.GateID
		}
		parts = append(parts, fmt.Sprintf("%s: %s", firstNonEmptyStr(e.GateID, "pipeline"), e.Code))
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("agent %s is blocked by its pipeline (%s)", firstNonEmptyStr(adm.Agent, adm.AgentID), strings.Join(parts, ", ")),
			"type":    "pipeline_blocked",
			"code":    "pipeline_blocked",
			"param":   nil,
			"gate_id": gate,
			"action":  adm.Action,
			"reasons": blocking,
		},
		"admission": adm,
	})
}

// blockedAudit rate-limits pipeline.blocked events per agent and reason set.
type blockedAudit struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (b *blockedAudit) allow(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil {
		b.last = map[string]time.Time{}
	}
	if t, ok := b.last[key]; ok && now.Sub(t) < blockedAuditInterval {
		return false
	}
	b.last[key] = now
	if len(b.last) > 4096 {
		for k, t := range b.last {
			if now.Sub(t) >= blockedAuditInterval {
				delete(b.last, k)
			}
		}
	}
	return true
}

func (b *blockedAudit) forget(key string) { b.mu.Lock(); defer b.mu.Unlock(); delete(b.last, key) }

func (s *Server) auditBlocked(r *http.Request, a *model.Agent, adm *model.Admission, issue string) {
	if s.Store == nil {
		return
	}
	var codes []string
	for _, e := range adm.Blocking() {
		codes = append(codes, firstNonEmptyStr(e.GateID, "-")+"="+e.Code)
	}
	key := a.ID + "|" + issue + "|" + strings.Join(codes, ",")
	if !s.blocked.allow(key, time.Now()) {
		return
	}
	if err := s.Store.AppendEvent(r.Context(), &model.Event{
		ID:        fmt.Sprintf("evt-pipeline-blocked-%d", time.Now().UnixNano()),
		Type:      model.EventPipelineBlocked,
		Actor:     actor(r),
		AgentID:   a.ID,
		Message:   fmt.Sprintf("agent %s blocked (issue %s): %s", a.Name, firstNonEmptyStr(issue, "-"), strings.Join(codes, ", ")),
		Timestamp: time.Now().UTC(),
	}); err != nil {
		s.blocked.forget(key)
	}
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
