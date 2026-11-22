package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

type blockingSessions struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingSessions) Prompt(ctx context.Context, _, _ string) (string, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return "done", nil
}

func (b *blockingSessions) Close(string) {}

func liveItems(t *testing.T, f *pipelineFixture) []model.LiveWork {
	t.Helper()
	rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("live as monitor: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Items []model.LiveWork `json:"items"`
		Now   time.Time        `json:"now"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Now.IsZero() {
		t.Fatalf("live body: %v %s", err, rec.Body)
	}
	return out.Items
}

// An endpoint job is listed while it runs (stable agent ID and issue) and
// disappears when it finishes; the graph pulses only on real work.
func TestLiveWorkListsEndpointJobWhileRunning(t *testing.T) {
	f := newPipelineFixture(t)
	bs := &blockingSessions{started: make(chan struct{}), release: make(chan struct{})}
	f.srv.Sessions = bs
	if items := liveItems(t, f); len(items) != 0 {
		t.Fatalf("idle daemon lists work: %+v", items)
	}
	done := make(chan int, 1)
	go func() {
		done <- f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("alpha"), issueHeader, "ISS-7").Code
	}()
	select {
	case <-bs.started:
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint job never started")
	}
	items := liveItems(t, f)
	if len(items) != 1 || items[0].Kind != model.LiveEndpointJob || items[0].AgentID != "ag_a" || items[0].IssueID != "ISS-7" || items[0].StartedAt.IsZero() {
		t.Fatalf("running job: %+v", items)
	}
	close(bs.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("job status %d", code)
	}
	if items := liveItems(t, f); len(items) != 0 {
		t.Fatalf("finished job still listed: %+v", items)
	}
}

// A blocked request never shows up as live work.
func TestLiveWorkIgnoresBlockedRequests(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Sessions = &fakeSessions{out: "x"}
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")); rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
	if items := liveItems(t, f); len(items) != 0 {
		t.Fatalf("blocked request listed: %+v", items)
	}
}

func TestLiveRegistryRemovesOnlyItsOwnEntry(t *testing.T) {
	var l liveRegistry
	a := l.begin(model.LiveWork{Kind: model.LiveGateRun, UpstreamAgentID: "ag_a"})
	b := l.begin(model.LiveWork{Kind: model.LiveEndpointJob, AgentID: "ag_b"})
	a()
	a() // idempotent
	got := l.snapshot()
	if len(got) != 1 || got[0].AgentID != "ag_b" || got[0].ID == "" {
		t.Fatalf("snapshot: %+v", got)
	}
	b()
	if len(l.snapshot()) != 0 {
		t.Fatal("registry not empty")
	}
}
