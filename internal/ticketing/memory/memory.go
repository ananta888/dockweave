// Package memory is an in-memory ticketing adapter. It is the reference
// implementation of the contract (internal/ticketing/contracttest) and the
// test double for the dispatcher; it is not registered for production use.
package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// Provider keeps tickets and comments in memory.
type Provider struct {
	name string
	mu   sync.Mutex
	seq  int
	tk   map[string]*ticketing.Ticket
	cm   map[string][]ticketing.Comment
	now  func() time.Time
}

// New returns an empty provider with the given name.
func New(name string) *Provider {
	if name == "" {
		name = "memory"
	}
	return &Provider{name: name, tk: map[string]*ticketing.Ticket{}, cm: map[string][]ticketing.Comment{}, now: time.Now}
}

func (p *Provider) Name() string { return p.name }
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

func (p *Provider) stamp() time.Time { return p.now().UTC() }

func clone(t *ticketing.Ticket) *ticketing.Ticket {
	c := *t
	if t.Assignee != nil {
		a := *t.Assignee
		c.Assignee = &a
	}
	if t.Parent != nil {
		r := *t.Parent
		c.Parent = &r
	}
	c.BlockedBy = append([]ticketing.Ref(nil), t.BlockedBy...)
	if t.CreatedBy != nil {
		a := *t.CreatedBy
		c.CreatedBy = &a
	}
	return &c
}

func (p *Provider) Get(_ context.Context, id string) (*ticketing.Ticket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tk[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	return clone(t), nil
}

func (p *Provider) List(_ context.Context, f ticketing.Filter) ([]ticketing.Ticket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []ticketing.Ticket
	for _, t := range p.tk {
		if !matches(t, f) {
			continue
		}
		out = append(out, *clone(t))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Ref.ID < out[j].Ref.ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func matches(t *ticketing.Ticket, f ticketing.Filter) bool {
	if len(f.Status) > 0 {
		ok := false
		for _, s := range f.Status {
			ok = ok || t.Status == s
		}
		if !ok {
			return false
		}
	}
	if f.Assignee != nil && (t.Assignee == nil || t.Assignee.Kind != f.Assignee.Kind || t.Assignee.ID != f.Assignee.ID) {
		return false
	}
	if f.Parent != nil && (t.Parent == nil || *t.Parent != *f.Parent) {
		return false
	}
	if !f.UpdatedSince.IsZero() && t.UpdatedAt.Before(f.UpdatedSince) {
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(f.Query)); q != "" &&
		!strings.Contains(strings.ToLower(t.Title), q) && !strings.Contains(strings.ToLower(t.Description), q) {
		return false
	}
	return true
}

func (p *Provider) Create(_ context.Context, n ticketing.NewTicket, by ticketing.Actor) (*ticketing.Ticket, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if n.Parent != nil {
		if n.Parent.Provider != p.name {
			return nil, fmt.Errorf("%w: parent belongs to provider %q", ticketing.ErrInvalid, n.Parent.Provider)
		}
		if _, ok := p.tk[n.Parent.ID]; !ok {
			return nil, fmt.Errorf("%w: parent %s", ticketing.ErrNotFound, n.Parent.ID)
		}
	}
	p.seq++
	id := "m" + strconv.Itoa(p.seq)
	now := p.stamp()
	author := by
	t := &ticketing.Ticket{
		Ref: ticketing.Ref{Provider: p.name, ID: id}, Identifier: "MEM-" + strconv.Itoa(p.seq),
		Title: n.Title, Description: n.Description, Status: n.Status, Priority: n.Priority,
		Assignee: n.Assignee, Parent: n.Parent, BlockedBy: n.BlockedBy, WorkspaceID: n.WorkspaceID, CreatedBy: &author,
		CreatedAt: now, UpdatedAt: now, Version: "1",
	}
	p.tk[id] = clone(t)
	return clone(t), nil
}

func (p *Provider) Update(_ context.Context, id string, u ticketing.Update, _ ticketing.Actor) (*ticketing.Ticket, error) {
	if err := u.Validate(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tk[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	if u.ExpectedVersion != "" && u.ExpectedVersion != t.Version {
		return nil, fmt.Errorf("%w: version %s, expected %s", ticketing.ErrConflict, t.Version, u.ExpectedVersion)
	}
	if u.Title != nil {
		t.Title = strings.TrimSpace(*u.Title)
	}
	if u.Description != nil {
		t.Description = *u.Description
	}
	if u.Status != nil {
		t.Status = *u.Status
	}
	if u.Priority != nil {
		t.Priority = *u.Priority
	}
	if u.Assignee != nil {
		a := *u.Assignee
		t.Assignee = &a
	}
	if u.ClearAssignee {
		t.Assignee = nil
	}
	v, _ := strconv.Atoi(t.Version)
	t.Version = strconv.Itoa(v + 1)
	t.UpdatedAt = p.stamp()
	return clone(t), nil
}

func (p *Provider) Comments(_ context.Context, id string) ([]ticketing.Comment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.tk[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	return append([]ticketing.Comment(nil), p.cm[id]...), nil
}

func (p *Provider) AddComment(ctx context.Context, id, body string, by ticketing.Actor) (*ticketing.Comment, error) {
	return p.AddCommentWithOrigin(ctx, id, body, by, "")
}

// AddCommentWithOrigin implements ticketing.OriginCommenter.
func (p *Provider) AddCommentWithOrigin(_ context.Context, id, body string, by ticketing.Actor, origin string) (*ticketing.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty comment", ticketing.ErrInvalid)
	}
	if !by.Valid() {
		return nil, fmt.Errorf("%w: invalid author", ticketing.ErrInvalid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tk[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	p.seq++
	c := ticketing.Comment{ID: "c" + strconv.Itoa(p.seq), Ticket: t.Ref, Author: by, Body: body, CreatedAt: p.stamp()}
	c.Origin = origin
	if c.Origin == "" {
		c.Origin = p.name + ":" + c.ID
	}
	p.cm[id] = append(p.cm[id], c)
	return &c, nil
}

func (p *Provider) Next(_ context.Context, agentID string) (*ticketing.Ticket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *ticketing.Ticket
	for _, t := range p.tk {
		if t.Assignee == nil || t.Assignee.Kind != ticketing.ActorAgent || t.Assignee.ID != agentID {
			continue
		}
		if (t.CheckedOutBy != "" && t.CheckedOutBy != agentID) || (t.Status != ticketing.StatusTodo && t.Status != ticketing.StatusInProgress) {
			continue
		}
		if best == nil || t.CreatedAt.Before(best.CreatedAt) || (t.CreatedAt.Equal(best.CreatedAt) && t.Ref.ID < best.Ref.ID) {
			best = t
		}
	}
	if best == nil {
		return nil, nil
	}
	return clone(best), nil
}

func (p *Provider) Checkout(_ context.Context, id, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("%w: empty agent", ticketing.ErrInvalid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tk[id]
	if !ok {
		return fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	if t.CheckedOutBy != "" && t.CheckedOutBy != agentID {
		return fmt.Errorf("%w: checked out by %s", ticketing.ErrConflict, t.CheckedOutBy)
	}
	t.CheckedOutBy = agentID
	return nil
}

func (p *Provider) Release(_ context.Context, id, agentID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tk[id]
	if !ok {
		return fmt.Errorf("%w: %s", ticketing.ErrNotFound, id)
	}
	if t.CheckedOutBy != agentID {
		return fmt.Errorf("%w: not checked out by %s", ticketing.ErrConflict, agentID)
	}
	t.CheckedOutBy = ""
	return nil
}
