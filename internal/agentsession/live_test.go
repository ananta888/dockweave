package agentsession

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
)

// TestLivePrompt drives a real running agent. Guarded by env so it never runs
// in CI:
//
//	DOCKWEAVE_LIVE_TEST=1 DOCKWEAVE_LIVE_AGENT=ag_xxx go test -run TestLivePrompt -v ./internal/agentsession/
func TestLivePrompt(t *testing.T) {
	if os.Getenv("DOCKWEAVE_LIVE_TEST") == "" {
		t.Skip("set DOCKWEAVE_LIVE_TEST=1 and DOCKWEAVE_LIVE_AGENT=<agent-id> to run")
	}
	agentID := os.Getenv("DOCKWEAVE_LIVE_AGENT")
	if agentID == "" {
		t.Fatal("DOCKWEAVE_LIVE_AGENT is required")
	}
	dc, err := dockerclient.New()
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	m := New(Config{
		Docker: dc,
		FindContainer: func(ctx context.Context, id string) (string, error) {
			return dc.FindByAgent(ctx, id)
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := m.Prompt(ctx, agentID, "Sag in einem Satz, welcher Agent du bist.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("=== response (%d bytes) ===\n%s", len(out), out)
}
