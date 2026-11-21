//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/model"
)

// TestGateExpressionPathInSandbox covers GP-013: the expression contract is
// evaluated by the real sandbox runner after the command ran.
func TestGateExpressionPathInSandbox(t *testing.T) {
	r, _, root := gateRunner(t)

	t.Run("expected failure passes with reason_true", func(t *testing.T) {
		ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
			"id": "expect-fail", "when": "after_run", "command": "exit 1", "cwd": ".", "timeout_sec": 60,
			"expression": "command != 0", "on_true": "allow", "reason_true": "failed as expected",
		}}}, nil)
		run, err := r.Run(bg, gateexec.Request{Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != model.GatePassed || len(run.Results) != 1 {
			t.Fatalf("run = %s %q %+v", run.Status, run.Reason, run.Results)
		}
		res := run.Results[0]
		if res.Status != "passed" || res.Exit != 1 || res.Reason != "failed as expected" {
			t.Fatalf("result = %+v", res)
		}
		if run.WorkspaceFingerprint == "" {
			t.Fatal("run has no workspace fingerprint")
		}
	})

	t.Run("budget without configuration fails closed", func(t *testing.T) {
		ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
			"id": "budgeted", "when": "after_run", "command": "exit 0", "cwd": ".", "timeout_sec": 60,
			"expression": "command == 0 && budget < 5",
		}}}, nil)
		run, err := r.Run(bg, gateexec.Request{Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != model.GateFailed || run.Results[0].Status != "error" ||
			!strings.Contains(run.Results[0].Reason, "budget is unavailable") {
			t.Fatalf("run = %s, result = %+v", run.Status, run.Results)
		}
	})

	t.Run("invalid expression is an error with reason", func(t *testing.T) {
		ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
			"id": "broken", "when": "after_run", "command": "exit 0", "cwd": ".", "timeout_sec": 60,
			"expression": "command ==",
		}}}, nil)
		run, err := r.Run(bg, gateexec.Request{Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		if run.Results[0].Status != "error" || !strings.HasPrefix(run.Results[0].Reason, "gate expression:") {
			t.Fatalf("result = %+v", run.Results)
		}
	})

	t.Run("legacy expression true passes", func(t *testing.T) {
		ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
			"id": "legacy", "when": "after_run", "command": "exit 0", "cwd": ".", "timeout_sec": 60,
			"expression": "true",
		}}}, nil)
		run, err := r.Run(bg, gateexec.Request{Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != model.GatePassed {
			t.Fatalf("run = %s %+v", run.Status, run.Results)
		}
	})

	t.Run("explicit phase runs only that phase", func(t *testing.T) {
		ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{
			map[string]any{"id": "pre", "when": "pre_run", "command": "exit 0", "cwd": ".", "timeout_sec": 60},
			map[string]any{"id": "post", "when": "after_run", "command": "exit 1", "cwd": ".", "timeout_sec": 60},
		}}, nil)
		run, err := r.Run(bg, gateexec.Request{Workspace: ws, Phases: []string{"pre_run"}, UpstreamAgentID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != model.GatePassed || len(run.Results) != 1 || run.Results[0].ID != "pre" ||
			len(run.Phases) != 1 || run.UpstreamAgentID != "alpha" {
			t.Fatalf("run = %+v", run)
		}
	})
}
