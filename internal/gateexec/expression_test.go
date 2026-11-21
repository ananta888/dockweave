package gateexec

import (
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/internal/model"
)

func TestEvaluateExpression(t *testing.T) {
	cases := []struct {
		expr    string
		exit    int
		budget  float64
		want    bool
		wantErr bool
	}{
		{"command == 0", 0, 0, true, false},
		{"command == 0", 1, 0, false, false},
		{"exit != 0", 1, 0, true, false},
		{"command == 0 && budget < 5", 0, 3, true, false},
		{"command == 0 && budget < 5", 0, 9, false, false},
		{"command == 0 || budget < 5", 1, 3, true, false},
		{"!(command != 0)", 0, 0, true, false},
		{"(command == 0) && (budget <= 10.5)", 0, 10, true, false},
		{"budget >= 1.5", 0, 2.0, true, false},
		{"command == 0", -1, 0, false, false},
	}
	for _, c := range cases {
		got, err := EvaluateExpression(c.expr, c.exit, c.budget)
		if (err != nil) != c.wantErr {
			t.Fatalf("expr %q: err = %v, wantErr %v", c.expr, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Fatalf("expr %q (exit=%d budget=%v) = %v, want %v", c.expr, c.exit, c.budget, got, c.want)
		}
	}
}

func TestEvaluateExpressionErrors(t *testing.T) {
	for _, expr := range []string{"", "unknown == 0", "command ==", "command == 0 )", "foo bar"} {
		if _, err := EvaluateExpression(expr, 0, 0); err == nil {
			t.Fatalf("expr %q: expected error", expr)
		}
	}
}

func TestEvaluateUnaryMinus(t *testing.T) {
	for expr, want := range map[string]bool{"exit == -1": true, "exit > -2": true, "-exit == 1": true, "exit == --1": false} {
		got, err := EvaluateExpression(expr, -1, 0)
		if err != nil || got != want {
			t.Fatalf("expr %q = %v, %v; want %v", expr, got, err, want)
		}
	}
	if _, err := EvaluateExpression("exit == -", 0, 0); err == nil {
		t.Fatal("dangling minus must be an error")
	}
}

func TestEvaluateBudgetUnavailableFailsClosed(t *testing.T) {
	env := ExprEnv{Exit: 0, BudgetUnavailable: "broker unreachable"}
	if _, err := Evaluate("command == 0 && budget < 5", env); err == nil || !strings.Contains(err.Error(), "broker unreachable") {
		t.Fatalf("expected budget-unavailable error, got %v", err)
	}
	if ok, err := Evaluate("command == 0", env); err != nil || !ok {
		t.Fatalf("expression without budget must not need it: %v %v", ok, err)
	}
}

func TestBudgetExprEnv(t *testing.T) {
	if env := budgetExprEnv(nil); env.BudgetAvailable || env.BudgetUnavailable == "" {
		t.Fatalf("nil budget: %+v", env)
	}
	if env := budgetExprEnv(&model.GateBudget{Unavailable: true, Reason: "budget check unavailable: x"}); env.BudgetAvailable || env.BudgetUnavailable != "budget check unavailable: x" {
		t.Fatalf("unavailable budget: %+v", env)
	}
	if env := budgetExprEnv(&model.GateBudget{CostDayUSD: 2.5}); !env.BudgetAvailable || env.Budget != 2.5 {
		t.Fatalf("available budget: %+v", env)
	}
}

func TestGateVerdict(t *testing.T) {
	avail := ExprEnv{Budget: 3, BudgetAvailable: true}
	cases := []struct {
		name       string
		gate       gatesync.Gate
		exit       int
		env        ExprEnv
		wantStatus string
		wantReason string
	}{
		{"no expression pass", gatesync.Gate{}, 0, avail, "passed", ""},
		{"no expression fail", gatesync.Gate{}, 2, avail, "failed", ""},
		{"expected failure passes", gatesync.Gate{Expression: "command != 0", OnTrue: "failed as expected"}, 1, avail, "passed", "failed as expected"},
		{"expression false", gatesync.Gate{Expression: "command == 0", OnFalse: "tests red"}, 1, avail, "failed", "tests red"},
		{"expression false default reason", gatesync.Gate{Expression: "budget < 1"}, 0, avail, "failed", "gate expression evaluated to false"},
		{"invalid expression", gatesync.Gate{Expression: "command =="}, 0, avail, "error", ""},
		{"budget unavailable", gatesync.Gate{Expression: "budget < 5"}, 0, ExprEnv{BudgetUnavailable: "no budget configured for this workspace"}, "error", ""},
	}
	for _, c := range cases {
		env := c.env
		env.Exit = c.exit
		status, reason := gateVerdict(c.gate, env)
		if status != c.wantStatus {
			t.Fatalf("%s: status = %q (%s), want %q", c.name, status, reason, c.wantStatus)
		}
		if c.wantStatus != "error" && reason != c.wantReason {
			t.Fatalf("%s: reason = %q, want %q", c.name, reason, c.wantReason)
		}
		if c.wantStatus == "error" && reason == "" {
			t.Fatalf("%s: error without reason", c.name)
		}
	}
}
