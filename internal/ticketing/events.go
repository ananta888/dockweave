package ticketing

import (
	"context"
	"fmt"
	"time"
)

// Event types (ADR 0003 D-5).
const (
	EventCreated       = "ticket.created"
	EventUpdated       = "ticket.updated"
	EventStatusChanged = "ticket.status_changed"
	EventAssigned      = "ticket.assigned"
	EventCommentAdded  = "ticket.comment_added"
	EventCheckedOut    = "ticket.checked_out"
	EventReleased      = "ticket.released"
	EventDeleted       = "ticket.deleted"
)

// Event is a domain event about a ticket.
type Event struct {
	Type   string         `json:"type"`
	Ticket Ref            `json:"ticket"`
	Actor  Actor          `json:"actor"`
	At     time.Time      `json:"at"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Publisher receives domain events (audit log, UI push, sync outbox).
// Publish must not block for long and must not fail the mutation.
type Publisher interface {
	Publish(ctx context.Context, e Event)
}

// PublisherFunc adapts a function.
type PublisherFunc func(ctx context.Context, e Event)

func (f PublisherFunc) Publish(ctx context.Context, e Event) { f(ctx, e) }

// Publishers fans an event out to several publishers.
func Publishers(ps ...Publisher) Publisher {
	return PublisherFunc(func(ctx context.Context, e Event) {
		for _, p := range ps {
			if p != nil {
				p.Publish(ctx, e)
			}
		}
	})
}

// Observe decorates p so that every successful mutation publishes an event,
// whoever triggers it (API, dispatcher, sync). Reads pass through.
func Observe(p Provider, pub Publisher) Provider {
	if pub == nil {
		return p
	}
	return &observed{Provider: p, pub: pub}
}

type observed struct {
	Provider
	pub Publisher
}

func (o *observed) emit(ctx context.Context, typ string, ref Ref, by Actor, detail map[string]any) {
	o.pub.Publish(ctx, Event{Type: typ, Ticket: ref, Actor: by, At: time.Now().UTC(), Detail: detail})
}

func (o *observed) Tickets() Tickets {
	t := o.Provider.Tickets()
	if t == nil {
		return nil
	}
	return &obsTickets{Tickets: t, o: o}
}

func (o *observed) Threads() Threads {
	t := o.Provider.Threads()
	if t == nil {
		return nil
	}
	return &obsThreads{Threads: t, o: o}
}

func (o *observed) Inbox() Inbox {
	in := o.Provider.Inbox()
	if in == nil {
		return nil
	}
	return &obsInbox{Inbox: in, o: o}
}

type obsTickets struct {
	Tickets
	o *observed
}

func (t *obsTickets) Create(ctx context.Context, n NewTicket, by Actor) (*Ticket, error) {
	tk, err := t.Tickets.Create(ctx, n, by)
	if err == nil {
		d := map[string]any{"title": tk.Title, "status": string(tk.Status)}
		if tk.Assignee != nil {
			d["assignee"] = *tk.Assignee
		}
		t.o.emit(ctx, EventCreated, tk.Ref, by, d)
	}
	return tk, err
}

func (t *obsTickets) Update(ctx context.Context, id string, u Update, by Actor) (*Ticket, error) {
	before, _ := t.Tickets.Get(ctx, id)
	tk, err := t.Tickets.Update(ctx, id, u, by)
	if err != nil {
		return tk, err
	}
	t.o.emit(ctx, EventUpdated, tk.Ref, by, map[string]any{"version": tk.Version})
	if before != nil && before.Status != tk.Status {
		t.o.emit(ctx, EventStatusChanged, tk.Ref, by, map[string]any{"from": string(before.Status), "to": string(tk.Status)})
	}
	if before != nil && !sameActor(before.Assignee, tk.Assignee) {
		d := map[string]any{}
		if tk.Assignee != nil {
			d["assignee"] = *tk.Assignee
		}
		t.o.emit(ctx, EventAssigned, tk.Ref, by, d)
	}
	return tk, nil
}

// Delete forwards to the provider when it can delete and emits the event.
func (t *obsTickets) Delete(ctx context.Context, id string, by Actor) error {
	d, ok := t.Tickets.(Deleter)
	if !ok {
		return fmt.Errorf("%w: delete", ErrUnsupported)
	}
	before, _ := t.Tickets.Get(ctx, id)
	if err := d.Delete(ctx, id, by); err != nil {
		return err
	}
	detail := map[string]any{}
	if before != nil {
		detail["title"] = before.Title
	}
	t.o.emit(ctx, EventDeleted, Ref{Provider: t.o.Name(), ID: id}, by, detail)
	return nil
}

func sameActor(a, b *Actor) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.ID == b.ID
}

type obsThreads struct {
	Threads
	o *observed
}

func (t *obsThreads) AddComment(ctx context.Context, id, body string, by Actor) (*Comment, error) {
	c, err := t.Threads.AddComment(ctx, id, body, by)
	if err == nil {
		t.o.emit(ctx, EventCommentAdded, c.Ticket, by, map[string]any{"comment_id": c.ID})
	}
	return c, err
}

// AddCommentWithOrigin forwards to the provider when it supports origins.
func (t *obsThreads) AddCommentWithOrigin(ctx context.Context, id, body string, by Actor, origin string) (*Comment, error) {
	oc, ok := t.Threads.(OriginCommenter)
	if !ok {
		return nil, fmt.Errorf("%w: comment origins", ErrUnsupported)
	}
	c, err := oc.AddCommentWithOrigin(ctx, id, body, by, origin)
	if err == nil {
		t.o.emit(ctx, EventCommentAdded, c.Ticket, by, map[string]any{"comment_id": c.ID, "origin": origin})
	}
	return c, err
}

type obsInbox struct {
	Inbox
	o *observed
}

func (in *obsInbox) Checkout(ctx context.Context, id, agentID string) error {
	err := in.Inbox.Checkout(ctx, id, agentID)
	if err == nil {
		in.o.emit(ctx, EventCheckedOut, Ref{Provider: in.o.Name(), ID: id}, Actor{Kind: ActorAgent, ID: agentID}, nil)
	}
	return err
}

func (in *obsInbox) Release(ctx context.Context, id, agentID string) error {
	err := in.Inbox.Release(ctx, id, agentID)
	if err == nil {
		in.o.emit(ctx, EventReleased, Ref{Provider: in.o.Name(), ID: id}, Actor{Kind: ActorAgent, ID: agentID}, nil)
	}
	return err
}
