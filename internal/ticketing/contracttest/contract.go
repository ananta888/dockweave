// Package contracttest is the behaviour every ticketing adapter must show
// (ADR 0003). Each adapter's tests call Run with a fresh provider; the same
// suite runs against native, Paperclip (fake or test instance) and memory,
// so the core can rely on identical semantics.
package contracttest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// Run executes the contract against providers built by newProvider (one
// fresh, empty provider per subtest).
func Run(t *testing.T, newProvider func(t *testing.T) ticketing.Provider) {
	t.Helper()
	human := ticketing.Actor{Kind: ticketing.ActorHuman, ID: "iss|alice", Name: "Alice"}
	agentA := ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_a"}
	agentB := ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_b"}
	ctx := context.Background()

	t.Run("create_get_roundtrip", func(t *testing.T) {
		p := newProvider(t)
		tk, err := p.Tickets().Create(ctx, ticketing.NewTicket{Title: "  Fix login  ", Description: "details",
			Priority: ticketing.PriorityHigh, Assignee: &agentA}, human)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Ref.Provider != p.Name() || tk.Ref.ID == "" || tk.Title != "Fix login" || tk.Status != ticketing.StatusTodo || tk.Version == "" {
			t.Fatalf("created: %+v", tk)
		}
		got, err := p.Tickets().Get(ctx, tk.Ref.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "Fix login" || got.Description != "details" || got.Priority != ticketing.PriorityHigh ||
			got.Assignee == nil || got.Assignee.Kind != ticketing.ActorAgent || got.Assignee.ID != agentA.ID {
			t.Fatalf("get: %+v", got)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Fatalf("timestamps: %+v", got)
		}
		if p.Capabilities().Authorship && (got.CreatedBy == nil || got.CreatedBy.ID != human.ID) {
			t.Fatalf("authorship: %+v", got.CreatedBy)
		}
	})

	t.Run("invalid_input_rejected", func(t *testing.T) {
		p := newProvider(t)
		for _, n := range []ticketing.NewTicket{{Title: "  "}, {Title: "x", Status: "weird"}, {Title: "x", Priority: "urgent"},
			{Title: "x", Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent}}} {
			if _, err := p.Tickets().Create(ctx, n, human); !errors.Is(err, ticketing.ErrInvalid) {
				t.Fatalf("%+v: err=%v, want ErrInvalid", n, err)
			}
		}
	})

	t.Run("not_found", func(t *testing.T) {
		p := newProvider(t)
		if _, err := p.Tickets().Get(ctx, "does-not-exist"); !errors.Is(err, ticketing.ErrNotFound) {
			t.Fatalf("get: %v", err)
		}
		title := "x"
		if _, err := p.Tickets().Update(ctx, "does-not-exist", ticketing.Update{Title: &title}, human); !errors.Is(err, ticketing.ErrNotFound) {
			t.Fatalf("update: %v", err)
		}
	})

	t.Run("update_fields_and_version", func(t *testing.T) {
		p := newProvider(t)
		tk := mustCreate(t, p, ticketing.NewTicket{Title: "a"}, human)
		st := ticketing.StatusInProgress
		title := "b"
		up, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st, Title: &title, Assignee: &agentB, ExpectedVersion: tk.Version}, human)
		if err != nil {
			t.Fatal(err)
		}
		if up.Status != st || up.Title != "b" || up.Assignee == nil || up.Assignee.ID != agentB.ID || up.Version == tk.Version {
			t.Fatalf("updated: %+v", up)
		}
		if !up.UpdatedAt.After(tk.UpdatedAt) && !up.UpdatedAt.Equal(tk.UpdatedAt) {
			t.Fatalf("updated_at went backwards")
		}
		// A stale version is a conflict and changes nothing.
		done := ticketing.StatusDone
		if _, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &done, ExpectedVersion: tk.Version}, human); !errors.Is(err, ticketing.ErrConflict) {
			t.Fatalf("stale version: %v", err)
		}
		cur, _ := p.Tickets().Get(ctx, tk.Ref.ID)
		if cur.Status != st {
			t.Fatalf("stale update applied: %s", cur.Status)
		}
		up2, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{ClearAssignee: true}, human)
		if err != nil || up2.Assignee != nil {
			t.Fatalf("clear assignee: %+v %v", up2, err)
		}
		bad := ticketing.Status("weird")
		if _, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &bad}, human); !errors.Is(err, ticketing.ErrInvalid) {
			t.Fatalf("invalid status: %v", err)
		}
	})

	t.Run("list_filters", func(t *testing.T) {
		p := newProvider(t)
		a := mustCreate(t, p, ticketing.NewTicket{Title: "alpha bug", Assignee: &agentA}, human)
		b := mustCreate(t, p, ticketing.NewTicket{Title: "beta feature", Assignee: &agentB}, human)
		c := mustCreate(t, p, ticketing.NewTicket{Title: "gamma", Status: ticketing.StatusBacklog}, human)
		all, err := p.Tickets().List(ctx, ticketing.Filter{})
		if err != nil || len(all) != 3 {
			t.Fatalf("all: %d %v", len(all), err)
		}
		mine, _ := p.Tickets().List(ctx, ticketing.Filter{Assignee: &agentA})
		if len(mine) != 1 || mine[0].Ref != a.Ref {
			t.Fatalf("by assignee: %+v", mine)
		}
		backlog, _ := p.Tickets().List(ctx, ticketing.Filter{Status: []ticketing.Status{ticketing.StatusBacklog}})
		if len(backlog) != 1 || backlog[0].Ref != c.Ref {
			t.Fatalf("by status: %+v", backlog)
		}
		q, _ := p.Tickets().List(ctx, ticketing.Filter{Query: "feature"})
		if len(q) != 1 || q[0].Ref != b.Ref {
			t.Fatalf("by query: %+v", q)
		}
		lim, _ := p.Tickets().List(ctx, ticketing.Filter{Limit: 2})
		if len(lim) != 2 {
			t.Fatalf("limit: %d", len(lim))
		}
		since := time.Now().Add(time.Hour)
		none, _ := p.Tickets().List(ctx, ticketing.Filter{UpdatedSince: since})
		if len(none) != 0 {
			t.Fatalf("updated_since in the future: %d", len(none))
		}
	})

	t.Run("children", func(t *testing.T) {
		p := newProvider(t)
		if !p.Capabilities().Children {
			t.Skip("no children capability")
		}
		parent := mustCreate(t, p, ticketing.NewTicket{Title: "epic"}, human)
		child := mustCreate(t, p, ticketing.NewTicket{Title: "part", Parent: &parent.Ref}, human)
		if child.Parent == nil || *child.Parent != parent.Ref {
			t.Fatalf("child parent: %+v", child.Parent)
		}
		kids, err := p.Tickets().List(ctx, ticketing.Filter{Parent: &parent.Ref})
		if err != nil || len(kids) != 1 || kids[0].Ref != child.Ref {
			t.Fatalf("children: %+v %v", kids, err)
		}
		missing := ticketing.Ref{Provider: p.Name(), ID: "nope"}
		if _, err := p.Tickets().Create(ctx, ticketing.NewTicket{Title: "orphan", Parent: &missing}, human); !errors.Is(err, ticketing.ErrInvalid) && !errors.Is(err, ticketing.ErrNotFound) {
			t.Fatalf("missing parent: %v", err)
		}
	})

	t.Run("threads_append_only", func(t *testing.T) {
		p := newProvider(t)
		if !p.Capabilities().Threads || p.Threads() == nil {
			t.Skip("no threads capability")
		}
		tk := mustCreate(t, p, ticketing.NewTicket{Title: "t"}, human)
		c1, err := p.Threads().AddComment(ctx, tk.Ref.ID, "first", human)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Threads().AddComment(ctx, tk.Ref.ID, "second", agentA); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Threads().AddComment(ctx, tk.Ref.ID, "   ", human); !errors.Is(err, ticketing.ErrInvalid) {
			t.Fatalf("empty comment: %v", err)
		}
		if _, err := p.Threads().AddComment(ctx, "nope", "x", human); !errors.Is(err, ticketing.ErrNotFound) {
			t.Fatalf("comment on missing ticket: %v", err)
		}
		cs, err := p.Threads().Comments(ctx, tk.Ref.ID)
		if err != nil || len(cs) != 2 || cs[0].ID != c1.ID || cs[0].Body != "first" || cs[1].Author.ID != agentA.ID || cs[0].Author.Kind != ticketing.ActorHuman {
			t.Fatalf("thread: %+v %v", cs, err)
		}
	})

	t.Run("inbox_checkout_exclusive", func(t *testing.T) {
		p := newProvider(t)
		if !p.Capabilities().Inbox || p.Inbox() == nil {
			t.Skip("no inbox capability")
		}
		in := p.Inbox()
		if next, err := in.Next(ctx, agentA.ID); err != nil || next != nil {
			t.Fatalf("empty inbox: %+v %v", next, err)
		}
		mustCreate(t, p, ticketing.NewTicket{Title: "done already", Status: ticketing.StatusDone, Assignee: &agentA}, human)
		mustCreate(t, p, ticketing.NewTicket{Title: "backlog", Status: ticketing.StatusBacklog, Assignee: &agentA}, human)
		mustCreate(t, p, ticketing.NewTicket{Title: "for b", Assignee: &agentB}, human)
		tk := mustCreate(t, p, ticketing.NewTicket{Title: "for a", Assignee: &agentA}, human)
		next, err := in.Next(ctx, agentA.ID)
		if err != nil || next == nil || next.Ref != tk.Ref {
			t.Fatalf("next: %+v %v", next, err)
		}
		if err := in.Checkout(ctx, tk.Ref.ID, agentA.ID); err != nil {
			t.Fatal(err)
		}
		if err := in.Checkout(ctx, tk.Ref.ID, agentA.ID); err != nil {
			t.Fatalf("re-checkout by holder: %v", err)
		}
		if err := in.Checkout(ctx, tk.Ref.ID, agentB.ID); !errors.Is(err, ticketing.ErrConflict) {
			t.Fatalf("second agent: %v", err)
		}
		// The holder may resume its own claim; nobody else is offered it.
		if next, _ := in.Next(ctx, agentA.ID); next != nil && next.Ref != tk.Ref {
			t.Fatalf("unexpected next while holding: %+v", next)
		}
		if next, _ := in.Next(ctx, agentB.ID); next != nil && next.Ref == tk.Ref {
			t.Fatalf("ticket held by A offered to B: %+v", next)
		}
		got, _ := p.Tickets().Get(ctx, tk.Ref.ID)
		if got.CheckedOutBy != agentA.ID {
			t.Fatalf("checked_out_by: %q", got.CheckedOutBy)
		}
		if err := in.Release(ctx, tk.Ref.ID, agentB.ID); !errors.Is(err, ticketing.ErrConflict) {
			t.Fatalf("release by non-holder: %v", err)
		}
		if err := in.Release(ctx, tk.Ref.ID, agentA.ID); err != nil {
			t.Fatal(err)
		}
		if err := in.Checkout(ctx, "nope", agentA.ID); !errors.Is(err, ticketing.ErrNotFound) {
			t.Fatalf("checkout missing: %v", err)
		}
	})

	t.Run("blocked_by_roundtrip", func(t *testing.T) {
		p := newProvider(t)
		dep := mustCreate(t, p, ticketing.NewTicket{Title: "dependency"}, human)
		tk := mustCreate(t, p, ticketing.NewTicket{Title: "after", BlockedBy: []ticketing.Ref{dep.Ref}}, human)
		got, err := p.Tickets().Get(ctx, tk.Ref.ID)
		if err != nil || len(got.BlockedBy) != 1 || got.BlockedBy[0] != dep.Ref {
			t.Fatalf("blocked_by: %+v %v", got.BlockedBy, err)
		}
	})

	t.Run("checkout_race_has_one_winner", func(t *testing.T) {
		p := newProvider(t)
		if !p.Capabilities().Inbox || p.Inbox() == nil {
			t.Skip("no inbox capability")
		}
		tk := mustCreate(t, p, ticketing.NewTicket{Title: "race"}, human)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := p.Inbox().Checkout(ctx, tk.Ref.ID, "ag_"+string(rune('a'+i))); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("checkout winners = %d, want 1", wins)
		}
	})
}

func mustCreate(t *testing.T, p ticketing.Provider, n ticketing.NewTicket, by ticketing.Actor) *ticketing.Ticket {
	t.Helper()
	tk, err := p.Tickets().Create(context.Background(), n, by)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}
