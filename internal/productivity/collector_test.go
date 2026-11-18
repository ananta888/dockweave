package productivity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

func TestCollectorRecordsRunningAgentsIdempotently(t *testing.T) {
	st, err := persistence.Open(filepath.Join(t.TempDir(), "collector.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	agent := &model.Agent{
		ID: "ag_1", Name: "backend", Image: "node:22-bookworm",
		State: model.AgentRunning,
	}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	stopped := &model.Agent{
		ID: "ag_2", Name: "idle", Image: "node:22-bookworm",
		State: model.AgentStopped,
	}
	if err := st.CreateAgent(ctx, stopped); err != nil {
		t.Fatal(err)
	}

	c := &Collector{Store: st}
	c.tick(ctx)
	c.tick(ctx)

	events, err := st.ListWorkEvents(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (running agent, one minute bucket)", len(events))
	}
	if events[0].Kind != model.EventAgentRun || events[0].AgentID != "ag_1" {
		t.Fatalf("event = %+v", events[0])
	}
	if events[0].DurationSeconds != 60 {
		t.Fatalf("duration = %d", events[0].DurationSeconds)
	}
}
