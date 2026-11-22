package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/dispatch"
	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/authz"
)

// DispatchSettings configure the daemon's dispatcher (ADR 0003 D-4/D-9).
type DispatchSettings struct {
	// DefaultWorkspace is the host path gates run in for tickets without a
	// DockWeave workspace (Paperclip tickets; like DOCKWEAVE_HOST_WORKSPACE).
	DefaultWorkspace string
	// Reviewers are agent ids or names that get the VERDICT contract.
	Reviewers []string
	// PromptTimeout bounds one agent turn.
	PromptTimeout time.Duration
	// Leads are agents (ids or names) that plan and delegate (TK-014).
	Leads []string
	// RoleAgents maps plan roles to agents (ids or names).
	RoleAgents map[string]string
	// ContainerWorkspace is the shared directory named in plans.
	ContainerWorkspace string
}

// NewDispatcher wires the provider-neutral dispatcher to the daemon: real
// admission, pipeline edges, sandboxed gates, agent driver and review store.
// waker wakes agents (provider wake or the native scheduler).
func (s *Server) NewDispatcher(set DispatchSettings, provider ticketing.Provider, waker dispatch.Waker) *dispatch.Dispatcher {
	reviewers := map[string]bool{}
	for _, r := range set.Reviewers {
		if r = strings.TrimSpace(r); r != "" {
			reviewers[r] = true
		}
	}
	resolve := func(nameOrID string) string {
		if s.Agents != nil {
			if a, err := s.Agents.Get(context.Background(), nameOrID); err == nil {
				return a.ID
			}
		}
		return nameOrID
	}
	leads := map[string]bool{}
	for _, l := range set.Leads {
		if l = strings.TrimSpace(l); l != "" {
			leads[l] = true
		}
	}
	roles := map[string]string{}
	for role, agent := range set.RoleAgents {
		if role, agent = strings.ToLower(strings.TrimSpace(role)), strings.TrimSpace(agent); role != "" && agent != "" {
			roles[role] = resolve(agent)
		}
	}
	return &dispatch.Dispatcher{
		Lead: dispatch.LeadConfig{
			IsLead: func(agentID string) bool {
				if leads[agentID] {
					return true
				}
				if s.Agents != nil {
					if a, err := s.Agents.Get(context.Background(), agentID); err == nil {
						return leads[a.Name] || leads[a.ID]
					}
				}
				return false
			},
			Roles:              roles,
			ContainerWorkspace: set.ContainerWorkspace,
			Policy: func(ctx context.Context, workspace string) dispatch.LeadPolicy {
				pol := dispatch.LeadPolicy{ReviewRequired: true}
				cfg, raw, err := gateexec.ReadConfig(ctx, workspace)
				if err != nil {
					return pol // unreadable: the strict default
				}
				var keys map[string]json.RawMessage
				if json.Unmarshal(raw, &keys) == nil {
					if _, ok := keys["review"]; ok {
						pol.ReviewRequired = cfg.Review.Required
					}
				}
				pol.ReviewMatch = cfg.Review.IssueMatch
				pol.HumanApproval = cfg.HumanApproval.Required
				return pol
			},
			Approved: func(ctx context.Context, key string) bool {
				a, err := s.Store.GetGateApproval(ctx, key)
				return err == nil && a != nil
			},
		},
		Tickets:  provider,
		Admit:    dispatchAdmitter{s},
		Pipeline: dispatchEdges{s},
		Gates:    dispatchGates{s},
		Driver:   dispatchDriver{s},
		Waker:    waker,
		Reviews:  dispatchReviews{s},
		Log:      s.Store,
		Config: dispatch.Config{
			PromptTimeout: set.PromptTimeout,
			Workspace: func(ctx context.Context, wsID string) string {
				if wsID != "" && s.Workspaces != nil {
					if ws, err := s.Workspaces.Get(ctx, wsID); err == nil {
						return ws.HostPath
					}
					return "" // unknown workspace: a DONE cannot be confirmed
				}
				return set.DefaultWorkspace
			},
			Reviewer: func(agentID string) bool {
				if reviewers[agentID] {
					return true
				}
				if s.Agents != nil {
					if a, err := s.Agents.Get(context.Background(), agentID); err == nil {
						return reviewers[a.Name] || reviewers[a.ID]
					}
				}
				return false
			},
		},
	}
}

// dispatchAdmitter is the read-only precheck (never consumes an override).
type dispatchAdmitter struct{ s *Server }

func (a dispatchAdmitter) Admit(ctx context.Context, agent string) (*model.Admission, error) {
	if a.s.Agents == nil {
		return nil, errors.New("agents service not configured")
	}
	ag, err := a.s.Agents.Get(ctx, agent)
	if err != nil {
		return nil, err
	}
	ev := *a.s.pipelineEvaluator()
	adm := ev.Admit(ctx, ag.ID)
	adm.Agent = ag.Name
	return adm, nil
}

type dispatchEdges struct{ s *Server }

func (e dispatchEdges) Edges(ctx context.Context) (int, []model.PipelineEdge, error) {
	st, err := e.s.Store.ListPipelineEdges(ctx, persistence.PipelineEdgeFilter{})
	if err != nil {
		return 0, nil, err
	}
	out := make([]model.PipelineEdge, 0, len(st.Edges))
	for _, e := range st.Edges {
		out = append(out, *e)
	}
	return st.Revision, out, nil
}

type dispatchGates struct{ s *Server }

func (g dispatchGates) Run(ctx context.Context, req dispatch.GateRequest) (*model.GateRun, error) {
	if g.s.Gates == nil {
		return nil, errors.New("gate runner is not configured")
	}
	done := g.s.live.begin(model.LiveWork{Kind: model.LiveGateRun, UpstreamAgentID: req.UpstreamAgentID, Workspace: req.Workspace, IssueID: req.IssueID})
	defer done()
	return g.s.Gates.Run(ctx, gateexec.Request{Workspace: req.Workspace, IssueID: req.IssueID, Actor: "dispatcher",
		UpstreamAgentID: req.UpstreamAgentID, Phases: req.Phases})
}

// dispatchDriver drives the agent exactly like the execution endpoint: the
// consuming admission runs right before the turn (fail closed), the turn is
// visible as live endpoint work.
type dispatchDriver struct{ s *Server }

func (d dispatchDriver) Prompt(ctx context.Context, agentID, ticketRef, prompt string) (string, error) {
	ag, err := d.s.Agents.Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	ev := *d.s.pipelineEvaluator()
	ev.WorkActor, ev.WorkIssue = "dispatcher", ticketRef
	adm := ev.Admit(ctx, ag.ID)
	adm.Agent = ag.Name
	if !adm.Allowed {
		return "", &dispatch.BlockedError{Admission: adm}
	}
	done := d.s.live.begin(model.LiveWork{Kind: model.LiveEndpointJob, AgentID: ag.ID, IssueID: ticketRef})
	defer done()
	return d.s.promptAgent(ctx, ag.Type, string(ag.Runtime), ag.ID, prompt)
}

type dispatchReviews struct{ s *Server }

func (r dispatchReviews) RecordVerdict(ctx context.Context, ticketRef, verdict, note, actor string) error {
	_, err := r.s.Store.UpsertGateReview(ctx, &model.GateReview{IssueID: ticketRef, Verdict: verdict, Note: note,
		Actor: "dispatcher:" + actor, RecordedAt: time.Now().UTC()})
	if errors.Is(err, persistence.ErrConflict) {
		return nil // a contradicting verdict is audited by the store
	}
	return err
}

// AgentWaker resolves agent names to ids and wakes through the provider
// (e.g. Paperclip) or, without provider wake, the native scheduler.
type AgentWaker struct {
	Server    *Server
	Provider  ticketing.Provider
	Scheduler *dispatch.Scheduler
}

func (w AgentWaker) Wake(ctx context.Context, agent, reason string) error {
	id := agent
	if w.Server != nil && w.Server.Agents != nil {
		if a, err := w.Server.Agents.Get(ctx, agent); err == nil {
			id = a.ID
		}
	}
	if w.Provider != nil && w.Provider.Waker() != nil {
		return w.Provider.Waker().Wake(ctx, id)
	}
	if w.Scheduler != nil {
		return w.Scheduler.Wake(ctx, id, reason)
	}
	return errors.New("no way to wake agents (provider without wake and no scheduler)")
}

// ---- endpoints ----

func (s *Server) dispatcherOrErr(w http.ResponseWriter) (*dispatch.Dispatcher, bool) {
	if _, ok := s.ticketPrimary(w); !ok {
		return nil, false
	}
	if s.Dispatcher == nil {
		writeErr(w, http.StatusServiceUnavailable, "ticketing_unavailable", errors.New("dispatcher is not configured"))
		return nil, false
	}
	return s.Dispatcher, true
}

// runTicketHeartbeat runs one dispatcher heartbeat for an agent (the
// Paperclip process adapter calls it via `dockweave ticket run <agent>`).
func (s *Server) runTicketHeartbeat(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dispatcherOrErr(w)
	if !ok {
		return
	}
	if !s.requirePerm(w, r, authz.AgentWake) {
		return
	}
	var req struct {
		Agent string `json:"agent"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		ticketErr(w, err)
		return
	}
	ag, err := s.Agents.Get(r.Context(), strings.TrimSpace(req.Agent))
	if err != nil {
		ticketErr(w, fmt.Errorf("%w: agent %q: %v", ticketing.ErrInvalid, req.Agent, err))
		return
	}
	out, err := d.RunOnce(r.Context(), ag.ID)
	if err != nil {
		ticketErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// routeTicket re-runs the pipeline routing for a ticket (the retry named in
// a "routing incomplete" note). Every effect is idempotent.
func (s *Server) routeTicket(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dispatcherOrErr(w)
	if !ok {
		return
	}
	if !s.requirePerm(w, r, authz.AgentWake) {
		return
	}
	p := s.Tickets.Primary
	id, err := ticketID(r, p)
	if err != nil {
		ticketErr(w, err)
		return
	}
	var req struct {
		Agent string `json:"agent"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		ticketErr(w, err)
		return
	}
	ag, err := s.Agents.Get(r.Context(), strings.TrimSpace(req.Agent))
	if err != nil {
		ticketErr(w, fmt.Errorf("%w: agent %q: %v", ticketing.ErrInvalid, req.Agent, err))
		return
	}
	tk, err := p.Tickets().Get(r.Context(), id)
	if err != nil {
		ticketErr(w, err)
		return
	}
	lines, rerr := d.Route(r.Context(), ag.ID, tk)
	out := map[string]any{"lines": lines, "complete": rerr == nil}
	if rerr != nil {
		out["error"] = rerr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
