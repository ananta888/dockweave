package memory

import (
	"context"
	"sync"
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/contracttest"
)

type recorder struct {
	mu  sync.Mutex
	evs []ticketing.Event
}

func (r *recorder) Publish(_ context.Context, e ticketing.Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}

func (r *recorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.evs {
		out = append(out, e.Type)
	}
	return out
}

// The decorator keeps the contract and emits one event per effective change.
func TestObservedProviderKeepsContract(t *testing.T) {
	contracttest.Run(t, func(*testing.T) ticketing.Provider { return ticketing.Observe(New("memory"), &recorder{}) })
}

func TestObservedProviderEmitsDomainEvents(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	p := ticketing.Observe(New("memory"), rec)
	alice := ticketing.Actor{Kind: ticketing.ActorHuman, ID: "iss|alice"}
	tk, err := p.Tickets().Create(ctx, ticketing.NewTicket{Title: "x"}, alice)
	if err != nil {
		t.Fatal(err)
	}
	st := ticketing.StatusInProgress
	agent := ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_a"}
	if _, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st, Assignee: &agent}, alice); err != nil {
		t.Fatal(err)
	}
	title := "y"
	if _, err := p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Title: &title}, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Threads().AddComment(ctx, tk.Ref.ID, "hi", alice); err != nil {
		t.Fatal(err)
	}
	_ = p.Inbox().Checkout(ctx, tk.Ref.ID, "ag_a")
	_ = p.Inbox().Checkout(ctx, tk.Ref.ID, "ag_b") // conflict: no event
	_ = p.Inbox().Release(ctx, tk.Ref.ID, "ag_a")
	// A failed mutation emits nothing.
	_, _ = p.Tickets().Update(ctx, "missing", ticketing.Update{Title: &title}, alice)
	want := []string{ticketing.EventCreated, ticketing.EventUpdated, ticketing.EventStatusChanged, ticketing.EventAssigned,
		ticketing.EventUpdated, ticketing.EventCommentAdded, ticketing.EventCheckedOut, ticketing.EventReleased}
	got := rec.types()
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
	if rec.evs[0].Actor != alice || rec.evs[0].Ticket != tk.Ref || rec.evs[2].Detail["to"] != "in_progress" {
		t.Fatalf("event content: %+v", rec.evs[:3])
	}
}
