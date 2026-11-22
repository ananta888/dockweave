package native

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/contracttest"
)

func openStore(t *testing.T) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(filepath.Join(t.TempDir(), "dw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNativeProviderContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) ticketing.Provider { return New(openStore(t)) })
}

// Existing productivity tasks become tickets without losing their nuance,
// and ticket updates never downgrade evidence-verified work.
func TestNativeKeepsProductivitySemantics(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	p := New(s)
	task := &model.Task{Title: "write docs", Priority: 80, Status: model.TaskPartial, Day: "2026-11-22"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	tk, err := p.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ticketing.StatusInProgress || tk.Priority != ticketing.PriorityHigh || tk.Identifier == "" || tk.Extra["native_status"] != "partial" {
		t.Fatalf("mapped: %+v", tk)
	}
	// A title change keeps the partial nuance and the day.
	title := "write the docs"
	if _, err := p.Update(ctx, task.ID, ticketing.Update{Title: &title}, ticketing.Actor{Kind: ticketing.ActorSystem}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.Status != model.TaskPartial || got.Day != "2026-11-22" || got.Priority != 80 {
		t.Fatalf("productivity fields changed: %+v", got)
	}
	got.Status = model.TaskVerified
	if err := s.UpdateTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	done := ticketing.StatusDone
	if _, err := p.Update(ctx, task.ID, ticketing.Update{Status: &done}, ticketing.Actor{Kind: ticketing.ActorSystem}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(ctx, task.ID); got.Status != model.TaskVerified {
		t.Fatalf("verified downgraded to %s", got.Status)
	}
	if tk, _ := p.Get(ctx, task.ID); tk.Extra["verified"] != true || tk.Status != ticketing.StatusDone {
		t.Fatalf("verified not visible: %+v", tk)
	}
}

// Checkout is a single conditional UPDATE: two stores on the same file (two
// processes) still have one winner.
func TestNativeCheckoutAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dw.db")
	s1, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	tk, err := New(s1).Create(ctx, ticketing.NewTicket{Title: "x"}, ticketing.Actor{Kind: ticketing.ActorSystem})
	if err != nil {
		t.Fatal(err)
	}
	if err := New(s1).Checkout(ctx, tk.Ref.ID, "ag_a"); err != nil {
		t.Fatal(err)
	}
	if err := New(s2).Checkout(ctx, tk.Ref.ID, "ag_b"); err == nil {
		t.Fatal("second process stole the checkout")
	}
}
