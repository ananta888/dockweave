//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/model"
)

// recordingSessions is an api.AgentSessions that records prompts.
type recordingSessions struct {
	mu      sync.Mutex
	prompts map[string]int
}

func (r *recordingSessions) Prompt(_ context.Context, idOrName, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prompts == nil {
		r.prompts = map[string]int{}
	}
	r.prompts[idOrName]++
	return "ok", nil
}
func (r *recordingSessions) Close(string) {}
func (r *recordingSessions) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prompts[id]
}

// TestPipelineEndToEnd covers GP-028: agent A -> sandbox gate -> agent B with
// the real runner, store, admission and OpenAI endpoint.
func TestPipelineEndToEnd(t *testing.T) {
	runner, store, root := gateRunner(t)
	now := time.Now().UTC()
	for _, a := range []*model.Agent{
		{ID: "ag_a", Name: "alpha", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ag_b", Name: "beta", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ag_c", Name: "gamma", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateAgent(bg, a); err != nil {
			t.Fatal(err)
		}
	}
	// The gate passes while ok.txt says "ok".
	ws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
		"id": "tests", "when": "after_run", "command": `grep -qx ok ok.txt`, "cwd": ".", "timeout_sec": 60,
		"on_false": "rework", "reason_false": "ok.txt must say ok",
	}}}, map[string]string{"ok.txt": "ok\n"})

	sessions := &recordingSessions{}
	srv := api.NewServer(&agents.Service{Store: store}, nil, nil, store)
	srv.Gates = runner
	srv.Sessions = sessions
	h := http.Handler(srv)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(api.ContextWithPeer(req.Context(), "admin", 0, api.RoleAdmin))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	chat := func(agent string) *httptest.ResponseRecorder {
		return call("POST", "/v1/chat/completions", `{"model":"`+agent+`","messages":[{"role":"user","content":"go"}]}`)
	}
	gate := func() *model.GateRun {
		run, err := runner.Run(bg, gateexec.Request{Workspace: ws, UpstreamAgentID: "alpha", Phases: []string{"after_run", "post_run"}})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	edges, _ := json.Marshal(map[string]any{"revision": 1, "edges": []any{map[string]any{
		"id": "pe-tests-beta", "gate_id": "tests", "upstream_agent_id": "alpha", "downstream_agent_id": "beta",
		"workspace": ws, "on_true": "allow", "on_false": "rework", "reason_false": "ok.txt must say ok",
	}}})
	if rec := call("PUT", "/api/v1/pipelines", string(edges)); rec.Code != http.StatusOK {
		t.Fatalf("push edges = %d %s", rec.Code, rec.Body)
	}

	// No run yet: B is blocked.
	if rec := chat("beta"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"no_run"`) {
		t.Fatalf("before any run = %d %s", rec.Code, rec.Body)
	}

	// Scenario PASS: A is done, the gate passes, B is admitted and driven.
	if run := gate(); run.Status != model.GatePassed {
		t.Fatalf("gate run = %s %s %+v", run.Status, run.Reason, run.Results)
	}
	if rec := call("GET", "/api/v1/pipelines/admission?agent=beta", ""); !strings.Contains(rec.Body.String(), `"allowed":true`) {
		t.Fatalf("admission after PASS = %s", rec.Body)
	}
	if rec := chat("beta"); rec.Code != http.StatusOK || sessions.count("ag_b") != 1 {
		t.Fatalf("chat after PASS = %d %s", rec.Code, rec.Body)
	}

	// Scenario STALE: a file changes after the PASS.
	if err := os.WriteFile(filepath.Join(ws, "notes.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := chat("beta"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"stale"`) {
		t.Fatalf("chat after change = %d %s", rec.Code, rec.Body)
	}

	// Scenario FAIL: the gate fails; B gets failed + rework.
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if run := gate(); run.Status != model.GateFailed {
		t.Fatalf("gate run = %s", run.Status)
	}
	rec := chat("beta")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"failed"`) ||
		!strings.Contains(rec.Body.String(), `"action":"rework"`) {
		t.Fatalf("chat after FAIL = %d %s", rec.Code, rec.Body)
	}
	if sessions.count("ag_b") != 1 {
		t.Fatal("blocked agent was prompted")
	}

	// Scenario Override: an audited override allows exactly one run.
	if rec := call("POST", "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix approved"}`); rec.Code != http.StatusOK {
		t.Fatalf("override = %d %s", rec.Code, rec.Body)
	}
	if rec := chat("beta"); rec.Code != http.StatusOK {
		t.Fatalf("chat with override = %d %s", rec.Code, rec.Body)
	}
	if rec := chat("beta"); rec.Code != http.StatusConflict {
		t.Fatalf("second chat with single-use override = %d", rec.Code)
	}

	// Tamper stays blocked even with a reusable override.
	tws := workspaceWithGates(t, root, map[string]any{"version": 1, "gates": []any{map[string]any{
		"id": "tamper", "when": "after_run", "command": `echo '{}' > .paperclip/gates.json`, "cwd": ".", "timeout_sec": 60,
	}}}, nil)
	edges2, _ := json.Marshal(map[string]any{"revision": 2, "edges": []any{map[string]any{
		"id": "pe-tamper-gamma", "gate_id": "tamper", "upstream_agent_id": "alpha", "downstream_agent_id": "gamma", "workspace": tws,
	}}})
	if rec := call("PUT", "/api/v1/pipelines", string(edges2)); rec.Code != http.StatusOK {
		t.Fatalf("push edges 2 = %d %s", rec.Code, rec.Body)
	}
	trun, err := runner.Run(bg, gateexec.Request{Workspace: tws})
	if err != nil || !trun.Tamper {
		t.Fatalf("tamper run = %+v %v", trun, err)
	}
	if rec := call("POST", "/api/v1/pipelines/override", `{"agent":"gamma","gate_id":"*","reason":"try anyway","single_use":false}`); rec.Code != http.StatusOK {
		t.Fatalf("override gamma = %d", rec.Code)
	}
	if rec := chat("gamma"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"tamper"`) {
		t.Fatalf("tamper chat = %d %s", rec.Code, rec.Body)
	}

	// Scenario no edge: beta lost its edge in revision 2, alpha never had one.
	if rec := chat("alpha"); rec.Code != http.StatusOK {
		t.Fatalf("ungated agent = %d %s", rec.Code, rec.Body)
	}
	if rec := chat("beta"); rec.Code != http.StatusOK {
		t.Fatalf("agent whose edge was removed = %d %s", rec.Code, rec.Body)
	}

	// Audit trail.
	events, err := store.ListEvents(bg, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[model.EventType]bool{}
	for _, e := range events {
		seen[e.Type] = true
	}
	for _, want := range []model.EventType{model.EventPipelineUpdate, model.EventPipelineBlocked,
		model.EventPipelineOverride, model.EventPipelineOverrideUsed, model.EventGateRun} {
		if !seen[want] {
			t.Errorf("missing audit event %s", want)
		}
	}
}
