package gateexec

import (
	"strings"
	"testing"
)

// RF-044: arbitrary input never panics, Validate and Evaluate agree on what
// is acceptable, and limits hold for both.
func FuzzExpression(f *testing.F) {
	for _, seed := range []string{"true", "command == 0", "!(exit != 0) && budget < 5", "((((1))))", "-!-1", "budget >",
		strings.Repeat("(", 200) + "1" + strings.Repeat(")", 200), strings.Repeat("!", 300) + "1", "1 &&", "foo == 1", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		_, verr := Validate(expr)
		_, eerr := Evaluate(expr, ExprEnv{Exit: 0, Budget: 1, BudgetAvailable: true})
		if strings.TrimSpace(expr) != "" && (verr == nil) != (eerr == nil) {
			t.Fatalf("validate/evaluate disagree for %q: validate=%v evaluate=%v", expr, verr, eerr)
		}
		if len(expr) > MaxExpressionBytes && verr == nil {
			t.Fatalf("oversized expression accepted (%d bytes)", len(expr))
		}
	})
}
