package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestGateRunAndApprovalRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	run := &model.GateRun{
		ID: "gate-1", Workspace: "/tmp/project", Image: "node:22", ImageDigest: "sha256:abc",
		Status: model.GateFailed, Gates: 2, Passed: 1, DurationMS: 42, GatesHash: "h",
		Tamper: true, Actor: "krusty", Reason: "gate tests failed", StartedAt: now, FinishedAt: now,
		Results: []model.GateResult{{ID: "tests", Command: "npm test", Status: "failed", Exit: 1, Output: "boom"}},
		Diff:    &model.GateDiff{OutOfScope: []string{"README.md"}},
	}
	if err := store.SaveGateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListGateRuns(ctx, run.Workspace, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	got := runs[0]
	if got.Status != model.GateFailed || !got.Tamper || got.Reason != "gate tests failed" {
		t.Fatalf("unexpected run: %+v", got)
	}
	if len(got.Results) != 1 || got.Results[0].Output != "boom" {
		t.Fatalf("results not persisted: %+v", got.Results)
	}
	if got.Diff == nil || len(got.Diff.OutOfScope) != 1 {
		t.Fatalf("diff not persisted: %+v", got.Diff)
	}

	if _, err := store.GetGateApproval(ctx, "issue-1"); err != ErrNotFound {
		t.Fatalf("missing approval err = %v, want ErrNotFound", err)
	}
	approval := &model.GateApproval{IssueID: "issue-1", Actor: "krusty", Role: "admin", Note: "ok", ApprovedAt: now}
	if err := store.SaveGateApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	approval.Note = "changed"
	if err := store.SaveGateApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	gotApproval, err := store.GetGateApproval(ctx, "issue-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotApproval.Note != "changed" || gotApproval.Actor != "krusty" || gotApproval.Role != "admin" {
		t.Fatalf("unexpected approval: %+v", gotApproval)
	}
}
