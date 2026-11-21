package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/pipeline"
)

type pipelineFixture struct {
	srv   *Server
	store *persistence.Store
	ws    string
	fp    string
	h     http.Handler
}

func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	for _, a := range []*model.Agent{
		{ID: "ag_a", Name: "alpha", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ag_b", Name: "beta", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	f := &pipelineFixture{store: store, ws: ws, fp: "sha256:one"}
	f.srv = NewServer(&agents.Service{Store: store}, nil, nil, store)
	f.srv.AllowedRoots = []string{filepath.Dir(ws)}
	f.srv.Pipeline = &pipeline.Evaluator{Store: store,
		Fingerprint: func(context.Context, string) (string, error) { return f.fp, nil }}
	f.h = f.srv.mux
	return f
}

func (f *pipelineFixture) do(t *testing.T, role Role, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(ContextWithPeer(req.Context(), string(role)+"-peer", 1000, role))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *pipelineFixture) pushEdges(t *testing.T, rev int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"revision": rev, "edges": []map[string]any{{
		"id": "pe-1", "gate_id": "tests", "upstream_agent_id": "alpha", "downstream_agent_id": "beta",
		"workspace": f.ws, "on_false": "rework", "reason_false": "fix tests",
	}}})
	return f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", string(body))
}

func (f *pipelineFixture) saveRun(t *testing.T, id string, status model.GateRunStatus, fp string) {
	t.Helper()
	ws, _ := filepath.EvalSymlinks(f.ws)
	now := time.Now().UTC()
	res := "passed"
	if status != model.GatePassed {
		res = "failed"
	}
	if err := f.store.SaveGateRun(context.Background(), &model.GateRun{
		ID: id, Workspace: ws, Status: status, StartedAt: now, FinishedAt: now, WorkspaceFingerprint: fp,
		Results: []model.GateResult{{ID: "tests", Status: res}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPutPipelinesRolesValidationAndRevision(t *testing.T) {
	f := newPipelineFixture(t)

	// monitor may not write (handler check; the middleware also blocks it).
	if rec := f.do(t, RoleMonitor, http.MethodPut, "/api/v1/pipelines", `{"revision":1,"edges":[]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("monitor PUT = %d", rec.Code)
	}
	rec := f.pushEdges(t, 2)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d %s", rec.Code, rec.Body)
	}
	var state model.PipelineState
	_ = json.Unmarshal(rec.Body.Bytes(), &state)
	if state.Revision != 2 || len(state.Edges) != 1 || state.Edges[0].DownstreamAgentID != "ag_b" || state.Edges[0].UpstreamAgentID != "ag_a" {
		t.Fatalf("state = %+v (agent names must be stored as ids)", state)
	}
	// Stale revision.
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stale_revision") {
		t.Fatalf("stale PUT = %d %s", rec.Code, rec.Body)
	}
	// Monitor may read.
	if rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/pipelines?agent=beta", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"gate_id":"tests"`) {
		t.Fatalf("monitor GET = %d %s", rec.Code, rec.Body)
	}

	bad := []string{
		`{"revision":3,"edges":[{"gate_id":"tests","downstream_agent_id":"ghost","workspace":"` + f.ws + `"}]}`,
		`{"revision":3,"edges":[{"gate_id":"tests","downstream_agent_id":"beta","workspace":"/etc"}]}`,
		`{"revision":3,"edges":[{"gate_id":"tests","downstream_agent_id":"beta","workspace":"relative"}]}`,
		`{"revision":3,"edges":[{"gate_id":"bad id","downstream_agent_id":"beta","workspace":"` + f.ws + `"}]}`,
		`{"revision":3,"edges":[{"gate_id":"tests","downstream_agent_id":"beta","workspace":"` + f.ws + `","on_true":"continue"}]}`,
		`{"revision":3,"edges":[{"gate_id":"tests","upstream_agent_id":"beta","downstream_agent_id":"beta","workspace":"` + f.ws + `"}]}`,
		`{"revision":-1,"edges":[]}`,
	}
	for _, b := range bad {
		if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", b); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid PUT %s = %d %s", b, rec.Code, rec.Body)
		}
	}
	// A rejected push leaves the old edges in place.
	if st, _ := f.store.ListPipelineEdges(context.Background(), persistence.PipelineEdgeFilter{}); st.Revision != 2 || len(st.Edges) != 1 {
		t.Fatalf("state changed by rejected push: %+v", st)
	}
	// Removing all edges works with a newer revision; reset accepts an older one.
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", `{"revision":5,"edges":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", `{"revision":1,"reset":true,"edges":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d", rec.Code)
	}
}

func TestAdmissionEndpointAndAgentStatus(t *testing.T) {
	f := newPipelineFixture(t)
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	get := func(path string) model.Admission {
		rec := f.do(t, RoleMonitor, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
		var adm model.Admission
		_ = json.Unmarshal(rec.Body.Bytes(), &adm)
		return adm
	}
	adm := get("/api/v1/pipelines/admission?agent=beta")
	if adm.Allowed || adm.Agent != "beta" || len(adm.Edges) != 1 || adm.Edges[0].Code != model.AdmitNoRun {
		t.Fatalf("no run: %+v", adm)
	}
	f.saveRun(t, "r1", model.GatePassed, "sha256:one")
	if adm := get("/api/v1/agents/beta/pipeline"); !adm.Allowed || adm.Edges[0].RunID != "r1" {
		t.Fatalf("pass: %+v", adm)
	}
	if adm := get("/api/v1/pipelines/admission?agent=alpha"); !adm.Allowed || len(adm.Edges) != 0 {
		t.Fatalf("upstream agent without edges must be allowed: %+v", adm)
	}
	rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/pipelines/admission", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"admissions"`) || !strings.Contains(rec.Body.String(), `"agent":"beta"`) {
		t.Fatalf("all admissions = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/pipelines/admission?agent=ghost", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent = %d", rec.Code)
	}
}

func chatBody(model string) string {
	return `{"model":"` + model + `","messages":[{"role":"user","content":"go"}]}`
}

func TestChatCompletionsEnforcesPipeline(t *testing.T) {
	f := newPipelineFixture(t)
	fs := &fakeSessions{out: "done"}
	f.srv.Sessions = fs
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	// Blocked: 409 pipeline_blocked, the agent is never prompted.
	rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"), issueHeader, "DOC-7")
	if rec.Code != http.StatusConflict {
		t.Fatalf("blocked chat = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Error struct {
			Code    string                `json:"code"`
			Type    string                `json:"type"`
			GateID  string                `json:"gate_id"`
			Reasons []model.AdmissionEdge `json:"reasons"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Code != "pipeline_blocked" || out.Error.Type != "pipeline_blocked" || out.Error.GateID != "tests" ||
		len(out.Error.Reasons) != 1 || out.Error.Reasons[0].Code != model.AdmitNoRun {
		t.Fatalf("error body = %s", rec.Body)
	}
	if fs.got != "" {
		t.Fatal("blocked agent was prompted")
	}
	// Streaming requests are blocked before the stream starts as well.
	stream := `{"model":"beta","stream":true,"messages":[{"role":"user","content":"go"}]}`
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", stream); rec.Code != http.StatusConflict || fs.got != "" {
		t.Fatalf("blocked stream = %d", rec.Code)
	}

	// Failed run: reason failed, action rework.
	f.saveRun(t, "r-fail", model.GateFailed, "sha256:one")
	rec = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"failed"`) || !strings.Contains(rec.Body.String(), `"action":"rework"`) {
		t.Fatalf("failed = %d %s", rec.Code, rec.Body)
	}

	// Passed on the current content: the agent is driven.
	time.Sleep(2 * time.Millisecond)
	f.saveRun(t, "r-pass", model.GatePassed, "sha256:one")
	rec = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	if rec.Code != http.StatusOK || fs.got != "go" {
		t.Fatalf("allowed = %d %s (prompt %q)", rec.Code, rec.Body, fs.got)
	}

	// Workspace changed: stale.
	fs.got = ""
	f.fp = "sha256:two"
	rec = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"stale"`) || fs.got != "" {
		t.Fatalf("stale = %d %s", rec.Code, rec.Body)
	}

	// Agents without edges are not affected.
	rec = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("alpha"))
	if rec.Code != http.StatusOK {
		t.Fatalf("ungated agent = %d %s", rec.Code, rec.Body)
	}

	// The block was audited once (rate limited) per reason set.
	events, err := f.store.ListEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	blocked := 0
	for _, e := range events {
		if e.Type == model.EventPipelineBlocked {
			blocked++
			if e.AgentID != "ag_b" {
				t.Fatalf("blocked event agent = %q", e.AgentID)
			}
		}
	}
	// One event per distinct (issue, reason set): no_run for DOC-7, no_run
	// without issue (the streamed retry), failed, stale.
	if blocked != 4 {
		t.Fatalf("pipeline.blocked events = %d, want 4", blocked)
	}
}

func TestChatCompletionsFailClosedOnAdmissionError(t *testing.T) {
	f := newPipelineFixture(t)
	fs := &fakeSessions{out: "done"}
	f.srv.Sessions = fs
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	f.saveRun(t, "r-pass", model.GatePassed, "sha256:one")
	// Fingerprint fails: blocked with fingerprint_unavailable.
	f.srv.Pipeline.Fingerprint = func(context.Context, string) (string, error) { return "", os.ErrPermission }
	rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "fingerprint_unavailable") || fs.got != "" {
		t.Fatalf("fingerprint error = %d %s", rec.Code, rec.Body)
	}
	// Store gone: internal, still blocked.
	f.srv.Pipeline = &pipeline.Evaluator{}
	rec = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"internal"`) || fs.got != "" {
		t.Fatalf("evaluator error = %d %s", rec.Code, rec.Body)
	}
}

func TestPipelineOverrideEndpoint(t *testing.T) {
	f := newPipelineFixture(t)
	fs := &fakeSessions{out: "done"}
	f.srv.Sessions = fs
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	f.saveRun(t, "r-fail", model.GateFailed, "sha256:one")

	if rec := f.do(t, RoleMonitor, http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("monitor override = %d", rec.Code)
	}
	for _, b := range []string{
		`{"agent":"beta","gate_id":"tests"}`,
		`{"agent":"beta","reason":"hotfix"}`,
		`{"agent":"beta","gate_id":"tests","reason":"hotfix","ttl_sec":999999}`,
	} {
		if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/pipelines/override", b); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid override %s = %d", b, rec.Code)
		}
	}
	rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix for prod"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("override = %d %s", rec.Code, rec.Body)
	}
	var o model.PipelineOverride
	_ = json.Unmarshal(rec.Body.Bytes(), &o)
	if !o.SingleUse || o.AgentID != "ag_b" || o.Actor != "admin-peer" || o.ExpiresAt.Sub(o.CreatedAt) != defaultOverrideTTL {
		t.Fatalf("override = %+v", o)
	}
	// Read-only admission does not consume it.
	for i := 0; i < 2; i++ {
		if rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/pipelines/admission?agent=beta", ""); !strings.Contains(rec.Body.String(), `"allowed":true`) {
			t.Fatalf("admission %d = %s", i, rec.Body)
		}
	}
	// Exactly one chat completion passes.
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")); rec.Code != http.StatusOK {
		t.Fatalf("override use = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")); rec.Code != http.StatusConflict {
		t.Fatalf("second use = %d", rec.Code)
	}
	if rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/pipelines/overrides?agent=beta", ""); !strings.Contains(rec.Body.String(), `"used_at"`) {
		t.Fatalf("overrides = %s", rec.Body)
	}
	events, _ := f.store.ListEvents(context.Background(), 100)
	var sawOverride, sawUse bool
	for _, e := range events {
		sawOverride = sawOverride || e.Type == model.EventPipelineOverride
		sawUse = sawUse || e.Type == model.EventPipelineOverrideUsed
	}
	if !sawOverride || !sawUse {
		t.Fatalf("audit override=%v used=%v", sawOverride, sawUse)
	}
}

func TestBlockedAuditRateLimit(t *testing.T) {
	var b blockedAudit
	now := time.Now()
	if !b.allow("k", now) || b.allow("k", now.Add(10*time.Second)) || !b.allow("other", now) {
		t.Fatal("rate limit within interval")
	}
	if !b.allow("k", now.Add(blockedAuditInterval+time.Second)) {
		t.Fatal("rate limit must expire")
	}
}

// RF-020: a block whose audit write failed is not remembered as reported, so
// the next block is audited instead of being suppressed by the rate limit.
func TestBlockedAuditForgetsFailedWrite(t *testing.T) {
	var b blockedAudit
	now := time.Now()
	if !b.allow("k", now) {
		t.Fatal("first block not allowed")
	}
	b.forget("k") // what auditBlocked does when AppendEvent fails
	if !b.allow("k", now.Add(time.Second)) {
		t.Fatal("failed audit write still suppresses the next audit")
	}
}
