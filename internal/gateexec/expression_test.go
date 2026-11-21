package gateexec

import "testing"

func TestEvaluateExpression(t *testing.T) {
	cases := []struct {
		expr   string
		exit   int
		budget float64
		want   bool
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
