package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/pipeline"
	"github.com/ananta888/dockweave/pkg/sdk"
)

// RF-001 end to end: gateway graph saves pushed to a real daemon API and
// store. A broken contract never weakens enforcement; deliberately disabling
// a connector does.
func TestGraphSavesAgainstRealDaemonKeepEnforcement(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, a := range []*model.Agent{
		{ID: "a", Name: "alpha", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "b", Name: "beta", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	daemon := api.NewServer(&agents.Service{Store: store}, nil, nil, store)
	daemon.AllowedRoots = []string{filepath.Dir(ws)}
	daemon.Pipeline = &pipeline.Evaluator{Store: store, Fingerprint: func(context.Context, string) (string, error) { return "fp", nil }}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemon.ServeHTTP(w, r.WithContext(api.ContextWithPeer(r.Context(), "gateway", 0, api.RoleAdmin)))
	}))
	defer hs.Close()
	client := sdk.NewHTTP(hs.URL)
	s := &Server{browseRoots: []string{ws}, pushPipeline: func(ctx context.Context, rev int, reset bool, edges []model.PipelineEdge) error {
		_, err := client.PutPipelines(ctx, rev, reset, edges)
		return err
	}}
	gate := func(id, contract string, enabled bool) map[string]any {
		return map[string]any{"id": id, "kind": "gate", "name": id, "enabled": enabled, "agentId": "a", "targetAgentId": "b", "contract": contract}
	}
	revision := 0
	save := func(conns ...map[string]any) PipelineSync {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"revision": revision, "connectors": conns,
			"paperclip": map[string]any{"workspace": ws, "syncGates": false}})
		rec := putGraphReq(s, string(body))
		var out struct {
			Revision int          `json:"revision"`
			Sync     PipelineSync `json:"pipeline_sync"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		revision = out.Revision
		return out.Sync
	}
	admitted := func() (*model.Admission, int) {
		adm, err := client.PipelineAdmission(context.Background(), "beta")
		if err != nil {
			t.Fatal(err)
		}
		st, _ := client.ListPipelines(context.Background(), "")
		return adm, len(st.Edges)
	}
	valid := `{"gate_id":"tests","command":"npm test"}`
	lint := `{"gate_id":"lint","command":"npm run lint"}`

	if sync := save(gate("g1", valid, true), gate("g2", lint, true)); !sync.Pushed {
		t.Fatalf("initial push: %+v", sync)
	}
	if adm, n := admitted(); adm.Allowed || n != 2 {
		t.Fatalf("without PASS beta must be blocked by 2 edges: allowed=%v edges=%d", adm.Allowed, n)
	}

	// One broken contract among several: nothing is published, both
	// previously enforced edges stay, and the error names the connector.
	sync := save(gate("g1", "{", true), gate("g2", lint, true))
	if sync.Pushed || sync.InvalidConnector == nil || sync.InvalidConnector.ConnectorID != "g1" {
		t.Fatalf("broken contract: %+v", sync)
	}
	if adm, n := admitted(); adm.Allowed || n != 2 {
		t.Fatalf("broken contract weakened enforcement: allowed=%v edges=%d", adm.Allowed, n)
	}

	// Deliberately disabling one connector is an explicit change.
	if sync := save(gate("g1", valid, false), gate("g2", lint, true)); !sync.Pushed {
		t.Fatalf("disable push: %+v", sync)
	}
	if adm, n := admitted(); adm.Allowed || n != 1 || adm.Edges[0].GateID != "lint" {
		t.Fatalf("after disabling g1: allowed=%v edges=%d %+v", adm.Allowed, n, adm.Edges)
	}

	// Removing the last gate releases beta explicitly.
	if sync := save(gate("g2", lint, false)); !sync.Pushed {
		t.Fatalf("remove push: %+v", sync)
	}
	if adm, n := admitted(); !adm.Allowed || n != 0 {
		t.Fatalf("explicit removal did not release beta: allowed=%v edges=%d", adm.Allowed, n)
	}
}
