// Package native is the DockWeave ticketing adapter (ADR 0003 D-2): tickets
// in the Paperclip style on top of the productivity task store. Evidence,
// work events, focus sessions and budgets keep working on the same rows.
package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// Name is the provider name in refs and configuration.
const Name = "native"

// Store is the subset of persistence.Store the adapter needs.
type Store interface {
	CreateTask(ctx context.Context, t *model.Task) error
	GetTask(ctx context.Context, id string) (*model.Task, error)
	UpdateTask(ctx context.Context, t *model.Task) error
	UpdateTaskIfVersion(ctx context.Context, t *model.Task, expected int) error
	ListTicketTasks(ctx context.Context, q persistence.TaskQuery) ([]*model.Task, error)
	CheckoutTask(ctx context.Context, id, agentID string) error
	ReleaseTask(ctx context.Context, id, agentID string) error
	AddTicketComment(ctx context.Context, c *model.TicketComment) error
	ListTicketComments(ctx context.Context, taskID string) ([]model.TicketComment, error)
}

// Provider implements ticketing.Provider on the DockWeave store.
type Provider struct {
	store Store
	now   func() time.Time
}

// New returns the native provider.
func New(store Store) *Provider { return &Provider{store: store, now: time.Now} }

// Register makes "native" selectable with the given store.
func Register(store Store) {
	ticketing.Register(Name, func(ticketing.Config) (ticketing.Provider, error) { return New(store), nil })
}

func (p *Provider) Name() string { return Name }

// Health is always OK: the store is local and opened by the daemon.
func (p *Provider) Health(context.Context) ticketing.Health {
	return ticketing.Health{OK: true, Checked: p.now().UTC()}
}

func (p *Provider) Capabilities() ticketing.Capabilities {
	return ticketing.Capabilities{Threads: true, Inbox: true, Children: true, Authorship: true}
}
func (p *Provider) Tickets() ticketing.Tickets { return p }
func (p *Provider) Threads() ticketing.Threads { return p }
func (p *Provider) Inbox() ticketing.Inbox     { return p }
func (p *Provider) Waker() ticketing.Waker     { return nil }

// ---- mapping (anti-corruption layer) ----

// toCanonical maps the richer productivity states onto the canonical set;
// the original state stays visible in Extra.
func toCanonical(s model.TaskStatus) ticketing.Status {
	switch s {
	case model.TaskBacklog:
		return ticketing.StatusBacklog
	case model.TaskTodo:
		return ticketing.StatusTodo
	case model.TaskInProgress, model.TaskPartial:
		return ticketing.StatusInProgress
	case model.TaskInReview:
		return ticketing.StatusInReview
	case model.TaskBlocked, model.TaskFailed:
		return ticketing.StatusBlocked
	case model.TaskDone, model.TaskVerified:
		return ticketing.StatusDone
	case model.TaskCancelled:
		return ticketing.StatusCancelled
	}
	return ticketing.StatusTodo
}

func fromCanonical(s ticketing.Status, current model.TaskStatus) model.TaskStatus {
	// "done" never downgrades an evidence-verified task.
	if s == ticketing.StatusDone && current == model.TaskVerified {
		return model.TaskVerified
	}
	if toCanonical(current) == s && current != "" {
		return current // keep partial/failed nuance when the canonical state is unchanged
	}
	return model.TaskStatus(s)
}

func priorityToInt(p ticketing.Priority, current int) int {
	switch p {
	case ticketing.PriorityCritical:
		return 95
	case ticketing.PriorityHigh:
		return 75
	case ticketing.PriorityMedium:
		return 50
	case ticketing.PriorityLow:
		return 25
	}
	return current
}

func priorityFromInt(v int) ticketing.Priority {
	switch {
	case v >= 90:
		return ticketing.PriorityCritical
	case v >= 70:
		return ticketing.PriorityHigh
	case v >= 40:
		return ticketing.PriorityMedium
	}
	return ticketing.PriorityLow
}

func (p *Provider) toTicket(t *model.Task) ticketing.Ticket {
	out := ticketing.Ticket{
		Ref:         ticketing.Ref{Provider: Name, ID: t.ID},
		Title:       t.Title,
		Description: t.Description,
		Status:      toCanonical(t.Status),
		Priority:    priorityFromInt(t.Priority),
		WorkspaceID: t.WorkspaceID,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		Version:     strconv.Itoa(t.Version),
		Extra:       map[string]any{"native_status": string(t.Status), "priority_value": t.Priority},
	}
	if t.TicketSeq > 0 {
		out.Identifier = "DW-" + strconv.Itoa(t.TicketSeq)
	}
	if t.Status == model.TaskVerified {
		out.Extra["verified"] = true
	}
	switch {
	case t.AssigneeKind == model.AssigneeAgent && t.AgentID != "":
		out.Assignee = &ticketing.Actor{Kind: ticketing.ActorAgent, ID: t.AgentID}
	case t.AssigneeID != "":
		out.Assignee = &ticketing.Actor{Kind: ticketing.ActorHuman, ID: t.AssigneeID, Name: t.AssigneeName}
	}
	if t.ParentID != "" {
		out.Parent = &ticketing.Ref{Provider: Name, ID: t.ParentID}
	}
	for _, b := range t.BlockedBy {
		out.BlockedBy = append(out.BlockedBy, ticketing.Ref{Provider: Name, ID: b})
	}
	if t.CreatedBy != "" {
		var a ticketing.Actor
		if json.Unmarshal([]byte(t.CreatedBy), &a) == nil && a.Valid() {
			out.CreatedBy = &a
		}
	}
	if t.CheckedOutBy != "" {
		out.CheckedOutBy = t.CheckedOutBy
	}
	return out
}

func setAssignee(t *model.Task, a *ticketing.Actor) {
	t.AssigneeID, t.AssigneeName, t.AgentID = "", "", ""
	t.AssigneeKind = model.AssigneeHuman
	if a == nil {
		return
	}
	if a.Kind == ticketing.ActorAgent {
		t.AssigneeKind, t.AgentID = model.AssigneeAgent, a.ID
		return
	}
	t.AssigneeID, t.AssigneeName = a.ID, a.Name
}

func mapErr(err error, id string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, persistence.ErrNotFound):
		return fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	case errors.Is(err, persistence.ErrConflict):
		return fmt.Errorf("%w: %v", ticketing.ErrConflict, err)
	}
	return err
}

// ---- Tickets ----

func (p *Provider) Get(ctx context.Context, id string) (*ticketing.Ticket, error) {
	t, err := p.store.GetTask(ctx, id)
	if err != nil {
		return nil, mapErr(err, id)
	}
	out := p.toTicket(t)
	return &out, nil
}

func (p *Provider) List(ctx context.Context, f ticketing.Filter) ([]ticketing.Ticket, error) {
	q := persistence.TaskQuery{UpdatedSince: f.UpdatedSince, Query: f.Query, Limit: f.Limit}
	for _, s := range f.Status {
		// One canonical state may cover several native ones.
		for _, ns := range []model.TaskStatus{model.TaskBacklog, model.TaskTodo, model.TaskInProgress, model.TaskPartial,
			model.TaskInReview, model.TaskBlocked, model.TaskFailed, model.TaskDone, model.TaskVerified, model.TaskCancelled} {
			if toCanonical(ns) == s {
				q.Statuses = append(q.Statuses, string(ns))
			}
		}
	}
	if len(f.Status) > 0 && len(q.Statuses) == 0 {
		return nil, nil
	}
	if f.Assignee != nil {
		if f.Assignee.Kind == ticketing.ActorAgent {
			q.AssigneeKind = string(model.AssigneeAgent)
		} else {
			q.AssigneeKind = string(model.AssigneeHuman)
		}
		q.AssigneeID = f.Assignee.ID
	}
	if f.Parent != nil {
		if f.Parent.Provider != Name {
			return nil, nil
		}
		q.ParentID = f.Parent.ID
	}
	tasks, err := p.store.ListTicketTasks(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]ticketing.Ticket, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, p.toTicket(t))
	}
	return out, nil
}

func (p *Provider) Create(ctx context.Context, n ticketing.NewTicket, by ticketing.Actor) (*ticketing.Ticket, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	t := &model.Task{Title: n.Title, Description: n.Description, Status: model.TaskStatus(n.Status),
		Priority: priorityToInt(n.Priority, 50), WorkspaceID: n.WorkspaceID}
	setAssignee(t, n.Assignee)
	if n.Parent != nil {
		if n.Parent.Provider != Name {
			return nil, fmt.Errorf("%w: parent belongs to provider %q", ticketing.ErrInvalid, n.Parent.Provider)
		}
		if _, err := p.store.GetTask(ctx, n.Parent.ID); err != nil {
			return nil, mapErr(err, n.Parent.ID)
		}
		t.ParentID = n.Parent.ID
	}
	for _, b := range n.BlockedBy {
		if b.Provider != Name {
			return nil, fmt.Errorf("%w: blocker belongs to provider %q", ticketing.ErrInvalid, b.Provider)
		}
		t.BlockedBy = append(t.BlockedBy, b.ID)
	}
	if by.Valid() {
		b, _ := json.Marshal(by)
		t.CreatedBy = string(b)
	}
	if err := p.store.CreateTask(ctx, t); err != nil {
		return nil, fmt.Errorf("%w: %v", ticketing.ErrInvalid, err)
	}
	return p.Get(ctx, t.ID)
}

func (p *Provider) Update(ctx context.Context, id string, u ticketing.Update, _ ticketing.Actor) (*ticketing.Ticket, error) {
	if err := u.Validate(); err != nil {
		return nil, err
	}
	t, err := p.store.GetTask(ctx, id)
	if err != nil {
		return nil, mapErr(err, id)
	}
	expected := 0
	if u.ExpectedVersion != "" {
		if expected, err = strconv.Atoi(u.ExpectedVersion); err != nil || expected <= 0 {
			return nil, fmt.Errorf("%w: version %q", ticketing.ErrInvalid, u.ExpectedVersion)
		}
		if expected != t.Version {
			return nil, fmt.Errorf("%w: version %d, expected %d", ticketing.ErrConflict, t.Version, expected)
		}
	}
	if u.Title != nil {
		t.Title = strings.TrimSpace(*u.Title)
	}
	if u.Description != nil {
		t.Description = *u.Description
	}
	if u.Status != nil {
		t.Status = fromCanonical(*u.Status, t.Status)
	}
	if u.Priority != nil {
		t.Priority = priorityToInt(*u.Priority, t.Priority)
	}
	if u.Assignee != nil {
		setAssignee(t, u.Assignee)
	}
	if u.ClearAssignee {
		setAssignee(t, nil)
	}
	if expected > 0 {
		err = p.store.UpdateTaskIfVersion(ctx, t, expected)
	} else {
		err = p.store.UpdateTask(ctx, t)
	}
	if err != nil {
		return nil, mapErr(err, id)
	}
	return p.Get(ctx, id)
}

// ---- Threads ----

func (p *Provider) Comments(ctx context.Context, id string) ([]ticketing.Comment, error) {
	cs, err := p.store.ListTicketComments(ctx, id)
	if err != nil {
		return nil, mapErr(err, id)
	}
	out := make([]ticketing.Comment, 0, len(cs))
	for _, c := range cs {
		out = append(out, ticketing.Comment{
			ID: c.ID, Ticket: ticketing.Ref{Provider: Name, ID: c.TaskID},
			Author: ticketing.Actor{Kind: ticketing.ActorKind(c.AuthorKind), ID: c.AuthorID, Name: c.AuthorName},
			Body:   c.Body, CreatedAt: c.CreatedAt, Origin: c.Origin,
		})
	}
	return out, nil
}

func (p *Provider) AddComment(ctx context.Context, id, body string, by ticketing.Actor) (*ticketing.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty comment", ticketing.ErrInvalid)
	}
	if !by.Valid() {
		return nil, fmt.Errorf("%w: invalid author", ticketing.ErrInvalid)
	}
	c := &model.TicketComment{TaskID: id, AuthorKind: string(by.Kind), AuthorID: by.ID, AuthorName: by.Name, Body: body}
	if err := p.store.AddTicketComment(ctx, c); err != nil {
		return nil, mapErr(err, id)
	}
	return &ticketing.Comment{ID: c.ID, Ticket: ticketing.Ref{Provider: Name, ID: id}, Author: by, Body: body,
		CreatedAt: c.CreatedAt, Origin: c.Origin}, nil
}

// ---- Inbox ----

func (p *Provider) Next(ctx context.Context, agentID string) (*ticketing.Ticket, error) {
	tasks, err := p.store.ListTicketTasks(ctx, persistence.TaskQuery{
		AssigneeKind: string(model.AssigneeAgent), AssigneeID: agentID,
		Statuses:    []string{string(model.TaskTodo), string(model.TaskInProgress), string(model.TaskPartial)},
		ClaimableBy: agentID, InboxOrder: true, Limit: 1,
	})
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	out := p.toTicket(tasks[0])
	return &out, nil
}

func (p *Provider) Checkout(ctx context.Context, id, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("%w: empty agent", ticketing.ErrInvalid)
	}
	return mapErr(p.store.CheckoutTask(ctx, id, agentID), id)
}

func (p *Provider) Release(ctx context.Context, id, agentID string) error {
	return mapErr(p.store.ReleaseTask(ctx, id, agentID), id)
}
