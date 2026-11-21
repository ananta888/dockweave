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

func TestGateReviewIdempotencyAndConflict(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	pass := &model.GateReview{IssueID: "DOC-1", Verdict: "PASS", Actor: "reviewer"}
	first, err := store.UpsertGateReview(ctx, pass)
	if err != nil {
		t.Fatal(err)
	}
	if first.EvidenceID == "" || first.Verdict != "PASS" {
		t.Fatalf("unexpected review: %+v", first)
	}
	again, err := store.UpsertGateReview(ctx, &model.GateReview{IssueID: "DOC-1", Verdict: "PASS", Actor: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if again.EvidenceID != first.EvidenceID {
		t.Fatalf("idempotent review created a second evidence row: %s != %s", again.EvidenceID, first.EvidenceID)
	}
	conflict, err := store.UpsertGateReview(ctx, &model.GateReview{IssueID: "DOC-1", Verdict: "FAIL", Actor: "reviewer"})
	if err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if !conflict.Conflict || conflict.Existing != "PASS" {
		t.Fatalf("unexpected conflict payload: %+v", conflict)
	}
	got, err := store.GetGateReview(ctx, "DOC-1")
	if err != nil || got.Verdict != "PASS" {
		t.Fatalf("get review: %+v %v", got, err)
	}
	list, err := store.ListGateReviews(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list reviews: %+v %v", list, err)
	}
}

func TestGateEvidenceNeedsNoTask(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inserted, err := store.AddEvidence(context.Background(), &model.Evidence{
		Kind: model.EvidenceGate, Status: model.EvidencePassed, Verified: true,
		IdempotencyKey: "gate-run:test",
	})
	if err != nil || !inserted {
		t.Fatalf("gate evidence without task rejected: inserted=%v err=%v", inserted, err)
	}
}

func TestPruneGateRuns(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	base := time.Now().UTC().Add(-48 * time.Hour)

	save := func(id, ws string, at time.Time) {
		if err := store.SaveGateRun(ctx, &model.GateRun{
			ID: id, Workspace: ws, Image: "node:22", Status: model.GatePassed,
			Gates: 1, Passed: 1, StartedAt: at, FinishedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	save("old-a1", "/ws-a", base.Add(-30*time.Hour))
	save("old-a2", "/ws-a", base.Add(-20*time.Hour))
	save("old-a3", "/ws-a", base.Add(-10*time.Hour))
	save("new-a", "/ws-a", base.Add(24*time.Hour))
	save("old-b1", "/ws-b", base.Add(-30*time.Hour))
	save("old-b2", "/ws-b", base.Add(-10*time.Hour))
	save("new-b", "/ws-b", base.Add(24*time.Hour))

	// Delete runs older than base, keeping the newest 1 per workspace. /ws-a
	// has 3 old runs, /ws-b has 2, so 2+1 = 3 old runs are removed.
	n, err := store.PruneGateRuns(ctx, base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pruned = %d, want 3", n)
	}
	runs, err := store.ListGateRuns(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Remaining: 1 kept old + 1 new per workspace = 4.
	if len(runs) != 4 {
		t.Fatalf("remaining runs = %d, want 4", len(runs))
	}
}
