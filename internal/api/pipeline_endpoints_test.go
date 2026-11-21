package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/nativeagent"
)

// RF-033: pipeline enforcement at every execution endpoint and role.

// nativeProbe counts how often a prompt actually reached the native adapter:
// FindContainer is the first step of every native prompt (opencode, herdr).
func nativeProbe(calls *atomic.Int32) *nativeagent.Manager {
	return &nativeagent.Manager{Timeout: time.Second, FindContainer: func(context.Context, string) (string, error) {
		calls.Add(1)
		return "", errors.New("probe: no container")
	}}
}

func addHerdrAgent(t *testing.T, f *pipelineFixture) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.store.CreateAgent(context.Background(), &model.Agent{ID: "ag_h", Name: "herd", Type: "codex", Image: "x",
		Runtime: model.RuntimeHerdr, State: model.AgentRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"revision":1,"edges":[{"id":"pe-b","gate_id":"tests","upstream_agent_id":"alpha","downstream_agent_id":"beta","workspace":%q},`+
		`{"id":"pe-h","gate_id":"tests","upstream_agent_id":"alpha","downstream_agent_id":"herd","workspace":%q}]}`, f.ws, f.ws)
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", body); rec.Code != http.StatusOK {
		t.Fatalf("push edges: %d %s", rec.Code, rec.Body)
	}
}

func TestBlockedWorkNeverReachesNativeAdapter(t *testing.T) {
	f := newPipelineFixture(t)
	var calls atomic.Int32
	f.srv.Native = nativeProbe(&calls)
	f.srv.Sessions = &fakeSessions{out: "must not run"}
	addHerdrAgent(t, f)

	for name, req := range map[string][2]string{
		"openai":        {"/v1/chat/completions", chatBody("beta")},
		"openai stream": {"/v1/chat/completions", `{"model":"beta","stream":true,"messages":[{"role":"user","content":"go"}]}`},
		"herdr":         {"/api/v1/agents/herd/herdr/prompt", `{"prompt":"go"}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(t, RoleAdmin, http.MethodPost, req[0], req[1])
			if rec.Code != http.StatusConflict {
				t.Fatalf("want 409, got %d %s", rec.Code, rec.Body)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("block must not start an event stream: %q", ct)
			}
			var out struct {
				Error struct {
					Code    string                `json:"code"`
					GateID  string                `json:"gate_id"`
					Action  string                `json:"action"`
					Reasons []model.AdmissionEdge `json:"reasons"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Error.Code != "pipeline_blocked" ||
				out.Error.GateID != "tests" || len(out.Error.Reasons) == 0 {
				t.Fatalf("unstructured block: %s", rec.Body)
			}
			if n := calls.Load(); n != 0 {
				t.Fatalf("blocked request reached the agent %d times", n)
			}
		})
	}

	// After a PASS on the current content both endpoints drive the agent.
	f.saveRun(t, "r-pass", model.GatePassed, "sha256:one")
	f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta"))
	f.do(t, RoleAdmin, http.MethodPost, "/api/v1/agents/herd/herdr/prompt", `{"prompt":"go"}`)
	if n := calls.Load(); n != 2 {
		t.Fatalf("allowed requests reached the agent %d times, want 2", n)
	}
	// Agents without incoming edges are unaffected.
	calls.Store(0)
	f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("alpha"))
	if calls.Load() != 1 {
		t.Fatal("ungated agent was not driven")
	}
}

func TestPipelineEndpointsThroughRealAuthMiddleware(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Sessions = &fakeSessions{out: "x"}
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	h := PeerAuthMiddleware(NewAuthPolicy([]uint32{0}, []uint32{2000}), f.srv.mux)
	call := func(uid uint32, method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		// Like PeerConnContext: only the uid is known; the policy resolves the role.
		req = req.WithContext(ContextWithPeer(req.Context(), "peer", uid, ""))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	writes := [][3]string{
		{http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"x"}`},
		{http.MethodPut, "/api/v1/pipelines", `{"revision":2,"edges":[]}`},
		{http.MethodPost, "/api/v1/gates/reviews", `{"issue":"DOC-1","verdict":"PASS"}`},
		{http.MethodPost, "/api/v1/gates/prune", `{"days":1}`},
		{http.MethodPost, "/v1/chat/completions", chatBody("alpha")},
	}
	for _, w := range writes {
		if code := call(3000, w[0], w[1], w[2]); code != http.StatusForbidden {
			t.Fatalf("unknown uid %s %s: %d", w[0], w[1], code)
		}
		if code := call(2000, w[0], w[1], w[2]); code != http.StatusForbidden {
			t.Fatalf("monitor %s %s: %d", w[0], w[1], code)
		}
	}
	if code := call(3000, http.MethodGet, "/api/v1/pipelines/admission?agent=beta", ""); code != http.StatusForbidden {
		t.Fatalf("unknown uid read: %d", code)
	}
	if code := call(2000, http.MethodGet, "/api/v1/pipelines/admission?agent=beta", ""); code != http.StatusOK {
		t.Fatalf("monitor read: %d", code)
	}
	// Admin is authorized but still subject to the pipeline.
	if code := call(0, http.MethodPost, "/v1/chat/completions", chatBody("beta")); code != http.StatusConflict {
		t.Fatalf("admin blocked chat: %d", code)
	}
	if code := call(0, http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix"}`); code != http.StatusOK {
		t.Fatalf("admin override: %d", code)
	}
}

func TestConcurrentPromptsConsumeSingleUseOverrideOnce(t *testing.T) {
	f := newPipelineFixture(t)
	var calls atomic.Int32
	f.srv.Native = nativeProbe(&calls)
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	f.saveRun(t, "r-fail", model.GateFailed, "sha256:one")
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")).Code
		}(i)
	}
	wg.Wait()
	blocked := 0
	for _, c := range codes {
		if c == http.StatusConflict {
			blocked++
		}
	}
	if n := calls.Load(); n != 1 || blocked != len(codes)-1 {
		t.Fatalf("single-use override executed %d times (codes %v)", n, codes)
	}
}

func TestOverrideNeverReleasesTamper(t *testing.T) {
	f := newPipelineFixture(t)
	var calls atomic.Int32
	f.srv.Native = nativeProbe(&calls)
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	ws, _ := filepath.EvalSymlinks(f.ws)
	now := time.Now().UTC()
	if err := f.store.SaveGateRun(context.Background(), &model.GateRun{ID: "r-tamper", Workspace: ws, Status: model.GateFailed,
		Tamper: true, StartedAt: now, FinishedAt: now, WorkspaceFingerprint: "sha256:one",
		Results: []model.GateResult{{ID: "tests", Status: "failed"}}}); err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"tests", "*"} {
		f.do(t, RoleAdmin, http.MethodPost, "/api/v1/pipelines/override", fmt.Sprintf(`{"agent":"beta","gate_id":%q,"reason":"try"}`, gate))
	}
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "tamper") || calls.Load() != 0 {
		t.Fatalf("tamper released: %d %s", rec.Code, rec.Body)
	}
}

func TestGateRunRejectsUnknownUpstreamAgent(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Gates = &gateexec.Runner{}
	rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/gates/run",
		fmt.Sprintf(`{"workspace":%q,"upstream_agent_id":"ghost"}`, f.ws))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ghost") {
		t.Fatalf("unknown upstream: %d %s", rec.Code, rec.Body)
	}
}

// RF-017: numeric limits are rejected before any duration arithmetic.
func TestNumericLimitsRejectedBeforeDurationConversion(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Gates = &gateexec.Runner{}
	for _, v := range []string{"-1", "86401", "9223372036854775807", "99999999999999999999"} {
		rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/gates/run", fmt.Sprintf(`{"workspace":%q,"timeout_sec":%s}`, f.ws, v))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("timeout_sec=%s: %d %s", v, rec.Code, rec.Body)
		}
	}
	for _, v := range []string{"-1", fmt.Sprint(model.MaxPipelineAgeSec + 1), "9223372036854775807"} {
		body := fmt.Sprintf(`{"revision":1,"edges":[{"id":"e","gate_id":"tests","downstream_agent_id":"beta","workspace":%q,"max_age_sec":%s}]}`, f.ws, v)
		if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("max_age_sec=%s: %d %s", v, rec.Code, rec.Body)
		}
	}
	ok := fmt.Sprintf(`{"revision":1,"edges":[{"id":"e","gate_id":"tests","downstream_agent_id":"beta","workspace":%q,"max_age_sec":%d}]}`, f.ws, model.MaxPipelineAgeSec)
	if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", ok); rec.Code != http.StatusOK {
		t.Fatalf("max_age_sec at the limit: %d %s", rec.Code, rec.Body)
	}
}

func TestPutPipelinesRejectsReleasingFailBranch(t *testing.T) {
	f := newPipelineFixture(t)
	for _, v := range []string{"allow", "wake:alpha"} {
		body := fmt.Sprintf(`{"revision":1,"edges":[{"id":"e","gate_id":"tests","downstream_agent_id":"beta","workspace":%q,"on_false":%q}]}`, f.ws, v)
		if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("on_false=%s: %d %s", v, rec.Code, rec.Body)
		}
	}
}

// RF-004: both branches keep action and reason up to the admission answer.
func TestRoutingMatrixKeepsActionAndReason(t *testing.T) {
	for _, tc := range []struct {
		name, onTrue, reasonTrue, onFalse, reasonFalse string
		status                                         model.GateRunStatus
		allowed                                        bool
		code, action, reason                           string
	}{
		{"pass allow", "allow", "fine", "", "", model.GatePassed, true, model.AdmitOK, "allow", "fine"},
		{"pass wake", "wake:alpha", "", "", "", model.GatePassed, true, model.AdmitOK, "wake:alpha", ""}, // target kept verbatim (id or name)
		{"pass deny", "deny", "hold for release", "", "", model.GatePassed, false, model.AdmitDenied, "deny", "hold for release"},
		{"pass rework", "rework", "review this", "", "", model.GatePassed, false, model.AdmitDenied, "rework", "review this"},
		{"fail deny", "", "", "deny", "tests red", model.GateFailed, false, model.AdmitFailed, "deny", "tests red"},
		{"fail rework", "", "", "rework", "fix tests", model.GateFailed, false, model.AdmitFailed, "rework", "fix tests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPipelineFixture(t)
			e := map[string]any{"id": "e", "gate_id": "tests", "upstream_agent_id": "alpha", "downstream_agent_id": "beta",
				"workspace": f.ws, "on_true": tc.onTrue, "reason_true": tc.reasonTrue, "on_false": tc.onFalse, "reason_false": tc.reasonFalse}
			body, _ := json.Marshal(map[string]any{"revision": 1, "edges": []any{e}})
			if rec := f.do(t, RoleAdmin, http.MethodPut, "/api/v1/pipelines", string(body)); rec.Code != http.StatusOK {
				t.Fatalf("push: %d %s", rec.Code, rec.Body)
			}
			f.saveRun(t, "r", tc.status, "sha256:one")
			var adm model.Admission
			rec := f.do(t, RoleAdmin, http.MethodGet, "/api/v1/pipelines/admission?agent=beta", "")
			if err := json.Unmarshal(rec.Body.Bytes(), &adm); err != nil || len(adm.Edges) != 1 {
				t.Fatalf("admission: %v %s", err, rec.Body)
			}
			ed := adm.Edges[0]
			if adm.Allowed != tc.allowed || ed.Code != tc.code || ed.Action != tc.action || ed.Reason != tc.reason || adm.Action != tc.action {
				t.Fatalf("got allowed=%v code=%s action=%s/%s reason=%q", adm.Allowed, ed.Code, ed.Action, adm.Action, ed.Reason)
			}
		})
	}
}

// RF-003: a reusable override (single_use=false) admits repeated work against
// the real store; reading the admission never consumes anything.
func TestReusableOverrideAdmitsRepeatedWork(t *testing.T) {
	f := newPipelineFixture(t)
	var calls atomic.Int32
	f.srv.Native = nativeProbe(&calls)
	if rec := f.pushEdges(t, 1); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	f.saveRun(t, "r-fail", model.GateFailed, "sha256:one")
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"*","reason":"hotfix window","single_use":false}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	for i := 0; i < 3; i++ {
		if rec := f.do(t, RoleAdmin, http.MethodPost, "/v1/chat/completions", chatBody("beta")); rec.Code == http.StatusConflict {
			t.Fatalf("use %d blocked: %s", i, rec.Body)
		}
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("reusable override admitted %d of 3 requests", n)
	}
}
