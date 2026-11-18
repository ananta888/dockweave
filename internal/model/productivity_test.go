package model

import (
	"testing"
	"time"
)

func TestTaskValidate(t *testing.T) {
	cases := []struct {
		name string
		task Task
		ok   bool
	}{
		{"valid human", Task{Title: "write", Day: "2026-11-18"}, true},
		{"empty title", Task{}, false},
		{"agent without id", Task{Title: "x", AssigneeKind: AssigneeAgent}, false},
		{"agent with id", Task{Title: "x", AssigneeKind: AssigneeAgent, AgentID: "ag_1"}, true},
		{"bad status", Task{Title: "x", Status: "nope"}, false},
		{"bad day", Task{Title: "x", Day: "18.11.2026"}, false},
		{"priority too high", Task{Title: "x", Priority: 101}, false},
	}
	for _, c := range cases {
		err := c.task.Validate()
		if c.ok && err != nil {
			t.Fatalf("%s: unexpected error %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s: expected error", c.name)
		}
	}
}

func TestObjectiveValidate(t *testing.T) {
	if err := (&Objective{Title: "Year", Kind: ObjectiveYear}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&Objective{Title: "x", Kind: "someday"}).Validate(); err == nil {
		t.Fatal("expected kind error")
	}
	if err := (&Objective{Kind: ObjectiveYear}).Validate(); err == nil {
		t.Fatal("expected title error")
	}
}

func TestInputBudgetValidate(t *testing.T) {
	ok := InputBudget{Scope: "objective", Kind: BudgetHumanHours, Period: "weekly", Target: 10}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := InputBudget{Scope: "team", Kind: BudgetTokens, Period: "daily"}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected scope error")
	}
}

func TestFocusElapsedIgnoresPauses(t *testing.T) {
	start := time.Now().UTC()
	f := FocusSession{StartedAt: start, PausedSecond: 30}
	got := f.Elapsed(start.Add(2 * time.Minute))
	if got.Minutes() != 1.5 {
		t.Fatalf("elapsed = %v, want 90s", got)
	}
}
