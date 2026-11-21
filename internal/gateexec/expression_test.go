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
		{"action plus reason_true", gatesync.Gate{Expression: "true", OnTrue: "wake:b", ReasonTrue: "green"}, 0, avail, "passed", "green"},
		{"action without reason", gatesync.Gate{Expression: "command == 0", OnFalse: "rework"}, 1, avail, "failed", "gate expression evaluated to false"},
		{"reason_false wins", gatesync.Gate{Expression: "false", OnFalse: "legacy", ReasonFalse: "explicit"}, 0, avail, "failed", "explicit"},
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

func TestEvaluateBooleanLiterals(t *testing.T) {
	cases := map[string]bool{
		"true":                  true,
		"false":                 false,
		"!false":                true,
		"!true":                 false,
		"true && exit == 0":     true,
		"true && command == 0":  true,
		"false || exit == 0":    true,
		"(true) && !(false)":    true,
		"true == 1":             true,
		"false && command == 0": false,
	}
	for expr, want := range cases {
		got, err := EvaluateExpression(expr, 0, 0)
		if err != nil || got != want {
			t.Fatalf("expr %q = %v, %v; want %v", expr, got, err, want)
		}
	}
	if got, err := EvaluateExpression("true && exit == 0", 1, 0); err != nil || got {
		t.Fatalf("true && exit == 0 with exit 1 = %v, %v", got, err)
	}
}

func TestDefaultTrueExpressionPassesGate(t *testing.T) {
	// graph.json documents created by older UIs carry expression "true".
	status, _ := gateVerdict(gatesync.Gate{Expression: "true"}, ExprEnv{Exit: 0})
	if status != "passed" {
		t.Fatalf("expression true: status %q, want passed", status)
	}
}

func TestValidate(t *testing.T) {
	v, err := Validate("command == 0 && budget < 5")
	if err != nil || !v.UsesBudget {
		t.Fatalf("Validate = %+v, %v; want uses_budget", v, err)
	}
	v, err = Validate("true && exit == 0")
	if err != nil || v.UsesBudget {
		t.Fatalf("Validate = %+v, %v", v, err)
	}
	for _, bad := range []string{"", "   ", "command ==", "foo == 1", "(command == 0", "command == 0 0"} {
		if _, err := Validate(bad); err == nil {
			t.Fatalf("Validate(%q): expected error", bad)
		}
	}
}

func TestNormalizePhases(t *testing.T) {
	got, err := normalizePhases([]string{"post_run", "pre_run,after_run", "pre_run"})
	if err != nil || strings.Join(got, ",") != "pre_run,after_run,post_run" {
		t.Fatalf("normalizePhases = %v, %v", got, err)
	}
	if got, err := normalizePhases(nil); err != nil || got != nil {
		t.Fatalf("empty = %v, %v", got, err)
	}
	if _, err := normalizePhases([]string{"during_run"}); err == nil {
		t.Fatal("unknown phase must fail")
	}
}
