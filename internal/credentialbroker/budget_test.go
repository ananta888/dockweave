package credentialbroker

import (
	"path/filepath"
	"testing"
	"time"
)

func testGrant(limits Limits) Grant {
	return Grant{ID: "g1", Agent: "a", Credential: "c", Provider: "openrouter",
		Models: []string{"*"}, Limits: limits}
}

func TestBudgetCommitAdjustsReservation(t *testing.T) {
	b, err := OpenBudget(filepath.Join(t.TempDir(), "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	g := testGrant(Limits{MaxRequestsPerMinute: 10, MaxConcurrent: 2})
	r, err := b.Acquire(g, 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	r.Commit(80, 20, g)
	u := b.Snapshot(g.ID)
	if u.RequestsDay != 1 || u.TokensDay != 100 {
		t.Fatalf("usage = %+v, want 1 request / 100 tokens", u)
	}
}

func TestBudgetReleaseRollsBack(t *testing.T) {
	b, _ := OpenBudget(filepath.Join(t.TempDir(), "budget.json"))
	g := testGrant(Limits{MaxRequestsPerMinute: 10, MaxConcurrent: 2})
	r, err := b.Acquire(g, 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
	u := b.Snapshot(g.ID)
	if u.RequestsDay != 0 || u.TokensDay != 0 {
		t.Fatalf("usage after release = %+v, want zero", u)
	}
}

func TestBudgetRequestLimit(t *testing.T) {
	b, _ := OpenBudget(filepath.Join(t.TempDir(), "budget.json"))
	g := testGrant(Limits{MaxRequestsPerMinute: 10, MaxConcurrent: 2, MaxRequestsPerDay: 1})
	r, err := b.Acquire(g, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
	r2, err := b.Acquire(g, 1, 1)
	if err != nil {
		t.Fatalf("request after release denied: %v", err)
	}
	if _, err := b.Acquire(g, 1, 1); err != ErrLimit {
		t.Fatalf("request over daily limit: %v", err)
	}
	r2.Release()
}

func TestBudgetConcurrencyAndRate(t *testing.T) {
	b, _ := OpenBudget(filepath.Join(t.TempDir(), "budget.json"))
	g := testGrant(Limits{MaxRequestsPerMinute: 1, MaxConcurrent: 1})
	r, err := b.Acquire(g, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(g, 1, 1); err != ErrLimit {
		t.Fatalf("concurrency limit not enforced: %v", err)
	}
	r.Release()
	if _, err := b.Acquire(g, 1, 1); err != ErrLimit {
		t.Fatalf("rate limit not enforced: %v", err)
	}
}

func TestBudgetDayRollover(t *testing.T) {
	b, _ := OpenBudget(filepath.Join(t.TempDir(), "budget.json"))
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return base }
	g := testGrant(Limits{MaxRequestsPerMinute: 10, MaxConcurrent: 2, MaxRequestsPerDay: 1})
	r, err := b.Acquire(g, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.Commit(1, 1, g)
	if _, err := b.Acquire(g, 1, 1); err != ErrLimit {
		t.Fatalf("daily limit not enforced: %v", err)
	}
	b.now = func() time.Time { return base.Add(24 * time.Hour) }
	if _, err := b.Acquire(g, 1, 1); err != nil {
		t.Fatalf("day rollover did not reset budget: %v", err)
	}
}
