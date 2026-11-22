package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/authz"
)

// providerView is the public status of one ticket provider.
type providerView struct {
	Name         string                 `json:"name"`
	Health       ticketing.Health       `json:"health"`
	Capabilities ticketing.Capabilities `json:"capabilities"`
}

func viewOf(ctx context.Context, p ticketing.Provider) *providerView {
	if p == nil {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return &providerView{Name: p.Name(), Health: p.Health(hctx), Capabilities: p.Capabilities()}
}

// ticketProvider reports the configured ticket providers (ADR 0003 D-3).
// Ticketing is opt-in: without TICKET_PROVIDER it says so instead of
// picking a provider silently.
func (s *Server) ticketProvider(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, authz.TicketRead) {
		return
	}
	out := map[string]any{"configured": s.Tickets != nil, "available": ticketing.Registered()}
	if s.TicketsError != "" {
		out["error"] = s.TicketsError
	}
	if s.Tickets != nil {
		out["primary"] = viewOf(r.Context(), s.Tickets.Primary)
		if s.Tickets.Mirror != nil {
			out["mirror"] = viewOf(r.Context(), s.Tickets.Mirror)
		}
	} else if s.TicketsError == "" {
		out["error"] = "ticketing is not configured: set TICKET_PROVIDER=native|paperclip in dockweaved.conf"
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- ticket API v1 (ADR 0003, TK-006) ----

// NewTicketAudit writes ticket domain events into the hash-chained audit log.
func NewTicketAudit(store *persistence.Store) ticketing.Publisher {
	return ticketing.PublisherFunc(func(ctx context.Context, e ticketing.Event) {
		b, _ := json.Marshal(map[string]any{"ticket": e.Ticket.String(), "actor": e.Actor, "detail": e.Detail})
		_ = store.AppendEvent(ctx, &model.Event{ID: model.NewID("evt"), Type: model.EventType(e.Type),
			Actor: e.Actor.Kind.String() + ":" + e.Actor.ID, Message: string(b), Timestamp: e.At})
	})
}

func (s *Server) ticketPrimary(w http.ResponseWriter) (ticketing.Provider, bool) {
	if s.Tickets == nil || s.Tickets.Primary == nil {
		msg := s.TicketsError
		if msg == "" {
			msg = "ticketing is not configured: set TICKET_PROVIDER=native|paperclip in dockweaved.conf"
		}
		writeErr(w, http.StatusServiceUnavailable, "ticketing_unavailable", errors.New(msg))
		return nil, false
	}
	return s.Tickets.Primary, true
}

func ticketErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ticketing.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err)
	case errors.Is(err, ticketing.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", err)
	case errors.Is(err, ticketing.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid", err)
	case errors.Is(err, ticketing.ErrUnsupported):
		writeErr(w, http.StatusNotImplemented, "unsupported", err)
	case errors.Is(err, ticketing.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "ticketing_unavailable", err)
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err)
	}
}

// ticketID accepts a bare id or "<provider>:<id>" of the primary provider.
func ticketID(r *http.Request, p ticketing.Provider) (string, error) {
	ref, err := ticketing.ParseRef(r.PathValue("id"), p.Name())
	if err != nil {
		return "", err
	}
	if ref.Provider != p.Name() {
		return "", fmt.Errorf("%w: ticket %s belongs to provider %q, primary is %q", ticketing.ErrNotFound, ref, ref.Provider, p.Name())
	}
	return ref.ID, nil
}

// requestActor is the caller as ticket actor: the human the web gateway
// forwarded (D-6), otherwise the authenticated socket peer (CLI, scripts).
func requestActor(r *http.Request) ticketing.Actor {
	if h, ok := HumanFromContext(r.Context()); ok {
		return ticketing.Actor{Kind: ticketing.ActorHuman, ID: h.ID, Name: h.Name}
	}
	return ticketing.Actor{Kind: ticketing.ActorHuman, ID: "peer:" + actor(r), Name: actor(r)}
}

func decodeStrict(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ticketing.ErrInvalid, err)
	}
	return nil
}

// resolveAgent maps a DockWeave agent name or id to its stable id; foreign
// provider agents ("paperclip-agent:…") pass through.
func (s *Server) resolveAgent(r *http.Request, a *ticketing.Actor) error {
	if a == nil || a.Kind != ticketing.ActorAgent || strings.Contains(a.ID, ":") || s.Agents == nil {
		return nil
	}
	ag, err := s.Agents.Get(r.Context(), a.ID)
	if err != nil {
		return fmt.Errorf("%w: agent %q: %v", ticketing.ErrInvalid, a.ID, err)
	}
	a.ID = ag.ID
	return nil
}

func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ticketPrimary(w)
	if !ok {
		return
	}
	if !s.requirePerm(w, r, authz.TicketRead) {
		return
	}
	q := r.URL.Query()
	var f ticketing.Filter
	for _, st := range strings.Split(q.Get("status"), ",") {
		if st = strings.TrimSpace(st); st != "" {
			f.Status = append(f.Status, ticketing.Status(st))
		}
	}
	if a := q.Get("assignee"); a != "" {
		kind, id, found := strings.Cut(a, ":")
		if !found || (kind != string(ticketing.ActorAgent) && kind != string(ticketing.ActorHuman)) || id == "" {
			ticketErr(w, fmt.Errorf("%w: assignee must be agent:<id> or human:<id>", ticketing.ErrInvalid))
			return
		}
		f.Assignee = &ticketing.Actor{Kind: ticketing.ActorKind(kind), ID: id}
		if err := s.resolveAgent(r, f.Assignee); err != nil {
			ticketErr(w, err)
			return
		}
	}
	if pr := q.Get("parent"); pr != "" {
		ref, err := ticketing.ParseRef(pr, p.Name())
		if err != nil {
			ticketErr(w, err)
			return
		}
		f.Parent = &ref
	}
	if us := q.Get("updated_since"); us != "" {
		t, err := time.Parse(time.RFC3339Nano, us)
		if err != nil {
			ticketErr(w, fmt.Errorf("%w: updated_since must be RFC 3339", ticketing.ErrInvalid))
			return
		}
		f.UpdatedSince = t
	}
	f.Query = q.Get("q")
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 || n > 1000 {
			ticketErr(w, fmt.Errorf("%w: limit must be 0..1000", ticketing.ErrInvalid))
			return
		}
		f.Limit = n
	}
	list, err := p.Tickets().List(r.Context(), f)
	if err != nil {
		ticketErr(w, err)
		return
	}
	if list == nil {
		list = []ticketing.Ticket{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": p.Name(), "tickets": list})
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ticketPrimary(w)
	if !ok {
		return
	}
	var n ticketing.NewTicket
	if err := decodeStrict(w, r, &n); err != nil {
		ticketErr(w, err)
		return
	}
	perms := []authz.Permission{authz.TicketCreate}
	if n.Assignee != nil {
		perms = append(perms, authz.TicketAssign)
	}
	if !s.requirePerm(w, r, perms...) {
		return
	}
	if err := s.resolveAgent(r, n.Assignee); err != nil {
		ticketErr(w, err)
		return
	}
	tk, err := p.Tickets().Create(r.Context(), n, requestActor(r))
	if err != nil {
		ticketErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tk)
}

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ticketPrimary(w)
	if !ok {
		return
	}
	if !s.requirePerm(w, r, authz.TicketRead) {
		return
	}
	id, err := ticketID(r, p)
	if err != nil {
		ticketErr(w, err)
		return
	}
	tk, err := p.Tickets().Get(r.Context(), id)
	if err != nil {
		ticketErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) updateTicket(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ticketPrimary(w)
	if !ok {
		return
	}
	id, err := ticketID(r, p)
	if err != nil {
		ticketErr(w, err)
		return
	}
	var u ticketing.Update
	if err := decodeStrict(w, r, &u); err != nil {
		ticketErr(w, err)
		return
	}
	var perms []authz.Permission
	if u.Title != nil || u.Description != nil || u.Status != nil || u.Priority != nil || (u.Assignee == nil && !u.ClearAssignee) {
		perms = append(perms, authz.TicketEdit)
	}
	if u.Assignee != nil || u.ClearAssignee {
		perms = append(perms, authz.TicketAssign)
	}
	if !s.requirePerm(w, r, perms...) {
		return
	}
	if err := s.resolveAgent(r, u.Assignee); err != nil {
		ticketErr(w, err)
		return
	}
	tk, err := p.Tickets().Update(r.Context(), id, u, requestActor(r))
	if err != nil {
		ticketErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) ticketComments(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ticketPrimary(w)
	if !ok {
		return
	}
	if p.Threads() == nil {
		ticketErr(w, fmt.Errorf("%w: threads", ticketing.ErrUnsupported))
		return
	}
	id, err := ticketID(r, p)
	if err != nil {
		ticketErr(w, err)
		return
	}
	perm := authz.TicketComment
	if r.Method == http.MethodGet {
		perm = authz.TicketRead
	}
	if !s.requirePerm(w, r, perm) {
		return
	}
	if r.Method == http.MethodGet {
		cs, err := p.Threads().Comments(r.Context(), id)
		if err != nil {
			ticketErr(w, err)
			return
		}
		if cs == nil {
			cs = []ticketing.Comment{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"comments": cs})
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		ticketErr(w, err)
		return
	}
	c, err := p.Threads().AddComment(r.Context(), id, req.Body, requestActor(r))
	if err != nil {
		ticketErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ticketClaim handles checkout and release for an agent.
func (s *Server) ticketClaim(release bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.ticketPrimary(w)
		if !ok {
			return
		}
		if !s.requirePerm(w, r, authz.TicketAssign) {
			return
		}
		if p.Inbox() == nil {
			ticketErr(w, fmt.Errorf("%w: inbox", ticketing.ErrUnsupported))
			return
		}
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
		a := &ticketing.Actor{Kind: ticketing.ActorAgent, ID: strings.TrimSpace(req.Agent)}
		if a.ID == "" {
			ticketErr(w, fmt.Errorf("%w: agent is required", ticketing.ErrInvalid))
			return
		}
		if err := s.resolveAgent(r, a); err != nil {
			ticketErr(w, err)
			return
		}
		if release {
			err = p.Inbox().Release(r.Context(), id, a.ID)
		} else {
			err = p.Inbox().Checkout(r.Context(), id, a.ID)
		}
		if err != nil {
			ticketErr(w, err)
			return
		}
		tk, err := p.Tickets().Get(r.Context(), id)
		if err != nil {
			ticketErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tk)
	}
}

// ---- permissions for humans (ADR 0003 D-6a, TK-013) ----

var defaultTicketPolicy = func() *authz.Policy {
	p, err := authz.Compile(authz.Default())
	if err != nil {
		panic(err)
	}
	return p
}()

func (s *Server) ticketPolicy() *authz.Policy {
	if s.TicketRoles == nil {
		return defaultTicketPolicy
	}
	return s.TicketRoles.Policy()
}

// requirePerm checks the permissions of a human forwarded by the gateway.
// Socket peers without a forwarded human (CLI, scripts) are governed by the
// peer role alone (ADMIN_UIDS/MONITOR_UIDS, enforced by the middleware).
func (s *Server) requirePerm(w http.ResponseWriter, r *http.Request, perms ...authz.Permission) bool {
	h, ok := HumanFromContext(r.Context())
	if !ok {
		return true
	}
	grant := s.ticketPolicy().For(h.Groups)
	for _, p := range perms {
		if !grant.Has(p) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":      map[string]string{"code": "forbidden", "message": "missing permission " + string(p)},
				"permission": p,
			})
			return false
		}
	}
	return true
}

// ticketMe tells the UI who the caller is and what it may do.
func (s *Server) ticketMe(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"actor": requestActor(r)}
	if h, ok := HumanFromContext(r.Context()); ok {
		g := s.ticketPolicy().For(h.Groups)
		out["roles"], out["permissions"], out["source"] = g.Roles, g.Permissions, "groups"
	} else {
		role, _ := r.Context().Value(peerRoleKey{}).(Role)
		perms := []authz.Permission{authz.TicketRead}
		if role == RoleAdmin {
			perms = authz.All
		}
		out["roles"], out["permissions"], out["source"] = []string{string(role)}, perms, "peer"
	}
	if s.TicketRoles != nil {
		if err := s.TicketRoles.Error(); err != nil {
			out["config_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}
