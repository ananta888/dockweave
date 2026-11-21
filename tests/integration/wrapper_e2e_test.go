//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/api"
	"github.com/ananta888/dockweave/internal/model"
)

// scriptedSessions stands in for the model: each agent answers with a fixed
// text. It is the only replaced part on the DockWeave side.
type scriptedSessions struct {
	mu      sync.Mutex
	answers map[string]string
	prompts map[string]int
}

func (s *scriptedSessions) Prompt(_ context.Context, id, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts[id]++
	return s.answers[id], nil
}
func (s *scriptedSessions) Close(string) {}
func (s *scriptedSessions) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prompts[id]
}
func (s *scriptedSessions) answer(id, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[id] = text
}

// fakePaperclipCLI logs every call as a JSON line and answers like the
// paperclipai CLI. Only this external system is simulated.
const fakePaperclipCLI = `#!/usr/bin/env python3
import json, os, sys
d = os.environ["FAKE_PC_DIR"]
with open(os.path.join(d, "pc.log"), "a") as fh:
    fh.write(json.dumps(sys.argv[1:]) + "\n")
a = sys.argv[1:]
if a[:2] == ["issue", "get"]:
    print(json.dumps({"id": a[2], "companyId": "co-1", "status": "in_progress"}))
elif a[:2] == ["issue", "list"]:
    print("[]")
elif a[:2] == ["issue", "create"]:
    print(json.dumps({"id": "rework-1", "identifier": "DOC-99"}))
else:
    print("{}")
`

type wrapperHarness struct {
	t        *testing.T
	repo     string
	env      []string
	pcDir    string
	sessions *scriptedSessions
	call     func(method, path, body string) *httptest.ResponseRecorder
	ws       string
}

func (h *wrapperHarness) runWrapper(agent string, extraEnv ...string) string {
	h.t.Helper()
	cmd := exec.Command("sh", filepath.Join(h.repo, "deployments/paperclip/dockweave-run.sh"), agent)
	cmd.Env = append(append([]string{}, h.env...), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("wrapper %s: %v\n%s", agent, err, out)
	}
	return string(out)
}

func (h *wrapperHarness) retryRoute() {
	h.t.Helper()
	cmd := exec.Command("python3", filepath.Join(h.repo, "deployments/paperclip/pipeline.py"),
		"route", "--upstream", "alpha", "--issue", "iss-1")
	cmd.Env = h.env
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("route retry: %v\n%s", err, out)
	}
}

func (h *wrapperHarness) paperclipCalls(verb ...string) [][]string {
	raw, _ := os.ReadFile(filepath.Join(h.pcDir, "pc.log"))
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var args []string
		if json.Unmarshal([]byte(line), &args) == nil && len(args) >= len(verb) &&
			strings.Join(args[:len(verb)], " ") == strings.Join(verb, " ") {
			out = append(out, args)
		}
	}
	return out
}

func (h *wrapperHarness) lastStatusUpdate() string {
	updates := h.paperclipCalls("issue", "update")
	if len(updates) == 0 {
		return ""
	}
	last := updates[len(updates)-1]
	for i := range last {
		if last[i] == "--status" && i+1 < len(last) {
			return last[i+1]
		}
	}
	return ""
}

func (h *wrapperHarness) gateRuns() []model.GateRun {
	rec := h.call("GET", "/api/v1/gates/runs?workspace="+h.ws+"&limit=50", "")
	var out struct {
		Runs []model.GateRun `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		h.t.Fatalf("gate runs: %v %s", err, rec.Body)
	}
	// Only completion runs; the worker also starts a pre_run run before every
	// turn (wrapper mode matrix).
	var done []model.GateRun
	for _, r := range out.Runs {
		for _, p := range r.Phases {
			if p == "after_run" {
				done = append(done, r)
				break
			}
		}
	}
	return done
}

func (h *wrapperHarness) lastComment() string {
	updates := h.paperclipCalls("issue", "update")
	if len(updates) == 0 {
		return ""
	}
	last := updates[len(updates)-1]
	for i := range last {
		if last[i] == "--comment" && i+1 < len(last) {
			return last[i+1]
		}
	}
	return ""
}

func newWrapperHarness(t *testing.T) *wrapperHarness {
	return newWrapperHarnessWithGate(t, `grep -qx ok ok.txt`)
}

func newWrapperHarnessWithGate(t *testing.T, gateCommand string) *wrapperHarness {
	return newWrapperHarnessWithConfig(t, gateCommand, nil)
}

func newWrapperHarnessWithConfig(t *testing.T, gateCommand string, extra map[string]any) *wrapperHarness {
	t.Helper()
	for _, bin := range []string{"python3", "curl", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			gatePrerequisite(t, bin+" not available")
		}
	}
	runner, store, root := gateRunner(t)
	now := time.Now().UTC()
	for _, a := range []*model.Agent{
		{ID: "ag_a", Name: "alpha", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ag_b", Name: "beta", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateAgent(bg, a); err != nil {
			t.Fatal(err)
		}
	}
	cfg := map[string]any{"version": 1, "gates": []any{map[string]any{
		"id": "tests", "when": "after_run", "command": gateCommand, "cwd": ".", "timeout_sec": 60,
	}}}
	for k, v := range extra {
		cfg[k] = v
	}
	ws := workspaceWithGates(t, root, cfg, map[string]string{"ok.txt": "ok\n"})

	sessions := &scriptedSessions{answers: map[string]string{}, prompts: map[string]int{}}
	srv := api.NewServer(&agents.Service{Store: store}, nil, nil, store)
	srv.Gates = runner
	srv.Sessions = sessions
	asAdmin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r.WithContext(api.ContextWithPeer(r.Context(), "admin", 0, api.RoleAdmin)))
	})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		asAdmin.ServeHTTP(rec, req)
		return rec
	}

	// Real daemon API on a unix socket, used by the real CLI and by curl.
	dir := t.TempDir()
	// Unix socket paths are limited to 108 bytes; t.TempDir() is too deep.
	sockDir, err := os.MkdirTemp("", "dw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: asAdmin}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })

	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "dockweave")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", cli, "./cmd/dockweave")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	// Paperclip: heartbeat inbox over HTTP and the paperclipai CLI.
	inbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
			"id": "iss-1", "identifier": "DOC-1", "title": "Implement feature"}}})
	}))
	t.Cleanup(inbox.Close)
	pcDir := filepath.Join(dir, "pc")
	if err := os.MkdirAll(pcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pc := filepath.Join(pcDir, "paperclipai")
	if err := os.WriteFile(pc, []byte(fakePaperclipCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	mapping := filepath.Join(dir, "agent-map.json")
	if err := os.WriteFile(mapping, []byte(`{"ag_a":"pc-a","ag_b":"pc-b"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	edges, _ := json.Marshal(map[string]any{"revision": 1, "edges": []any{map[string]any{
		"id": "pe-tests-beta", "gate_id": "tests", "upstream_agent_id": "alpha", "downstream_agent_id": "beta",
		"workspace": ws, "on_true": "allow", "on_false": "rework", "reason_false": "ok.txt must say ok",
	}}})
	if rec := call("PUT", "/api/v1/pipelines", string(edges)); rec.Code != http.StatusOK {
		t.Fatalf("push edges = %d %s", rec.Code, rec.Body)
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "FAKE_PC_DIR=" + pcDir,
		"DOCKWEAVE_PAPERCLIP_ENV=" + filepath.Join(dir, "none.env"),
		"DOCKWEAVE_SOCKET=" + sock, "DOCKWEAVE_CLI=" + cli, "PAPERCLIPAI_BIN=" + pc,
		"PAPERCLIP_API_URL=" + inbox.URL, "PAPERCLIP_API_KEY=test-key", "PAPERCLIP_COMPANY_ID=co-1",
		"DOCKWEAVE_HOST_WORKSPACE=" + ws, "DOCKWEAVE_PAPERCLIP_AGENT_MAP=" + mapping,
		"DOCKWEAVE_PIPELINE_STATE_DIR=" + filepath.Join(dir, "state"),
		"DOCKWEAVE_PAPERCLIP_HELPER_DIR=" + filepath.Join(repo, "deployments/paperclip"),
	}
	return &wrapperHarness{t: t, repo: repo, env: env, pcDir: pcDir, sessions: sessions, call: call, ws: ws}
}

// TestWrapperDoneDrivesGateAndReleasesDownstream covers RF-035 (GP-028 "A
// reports DONE"): the real dockweave-run.sh, CLI, daemon API, store,
// admission and sandbox gate runner. Simulated are only the model answer
// (scriptedSessions) and Paperclip (inbox server, paperclipai script).
func TestWrapperDoneDrivesGateAndReleasesDownstream(t *testing.T) {
	h := newWrapperHarness(t)

	// A answers without DONE: no gate run, no routing.
	h.sessions.answer("ag_a", "Teilarbeit erledigt\nSTATUS: IN_PROGRESS")
	h.runWrapper("alpha")
	if runs := h.gateRuns(); len(runs) != 0 {
		t.Fatalf("non-DONE answer started %d gate runs", len(runs))
	}
	if len(h.paperclipCalls("agent", "wake")) != 0 || h.lastStatusUpdate() != "in_progress" {
		t.Fatalf("non-DONE answer routed: %v", h.paperclipCalls())
	}

	// A reports DONE on a passing workspace: gate runs with A as upstream,
	// B is woken in Paperclip and admitted at the real endpoint.
	h.sessions.answer("ag_a", "fertig\nSTATUS: DONE")
	out := h.runWrapper("alpha")
	runs := h.gateRuns()
	if len(runs) != 1 || runs[0].Status != model.GatePassed || runs[0].UpstreamAgentID != "ag_a" ||
		runs[0].IssueID != "iss-1" {
		t.Fatalf("gate run after DONE: %+v\n%s", runs, out)
	}
	if got := h.lastStatusUpdate(); got != "done" {
		t.Fatalf("issue status after PASS = %q\n%s", got, out)
	}
	wakes := h.paperclipCalls("agent", "wake")
	if len(wakes) != 1 || wakes[0][2] != "pc-b" {
		t.Fatalf("downstream wake: %v\n%s", wakes, out)
	}
	h.sessions.answer("ag_b", "B arbeitet\nSTATUS: IN_PROGRESS")
	h.runWrapper("beta")
	if h.sessions.count("ag_b") != 1 {
		t.Fatalf("released B was not driven (prompts %d)", h.sessions.count("ag_b"))
	}

	// Retrying the routing of the same gate run (the command the wrapper puts
	// into the issue after a routing failure) wakes nobody twice. A new DONE
	// is a new gate run and may wake again by design (one wake per run).
	h.retryRoute()
	if n := len(h.paperclipCalls("agent", "wake")); n != 1 {
		t.Fatalf("route retry woke %d times", n)
	}

	// Stale: the workspace changes after the PASS; B is not driven.
	if err := os.WriteFile(filepath.Join(h.ws, "notes.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.runWrapper("beta")
	if h.sessions.count("ag_b") != 1 {
		t.Fatal("B was driven on stale content")
	}

	// FAIL: A's DONE is rejected, a rework issue goes to A, B stays blocked.
	if err := os.WriteFile(filepath.Join(h.ws, "ok.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = h.runWrapper("alpha")
	if got := h.lastStatusUpdate(); got != "in_progress" {
		t.Fatalf("issue status after FAIL = %q\n%s", got, out)
	}
	creates := h.paperclipCalls("issue", "create")
	if len(creates) != 1 || !strings.Contains(strings.Join(creates[0], " "), "--assignee-agent-id pc-a") {
		t.Fatalf("rework issue: %v\n%s", creates, out)
	}
	h.retryRoute()
	if n := len(h.paperclipCalls("issue", "create")); n != 1 {
		t.Fatalf("route retry created %d rework issues", n)
	}
	h.runWrapper("beta")
	if h.sessions.count("ag_b") != 1 {
		t.Fatal("B was driven after a failed gate")
	}
}

// RF-030/RF-035: without DOCKWEAVE_HOST_WORKSPACE a DONE cannot be checked;
// the issue stays open and nothing is routed.
func TestWrapperDoneWithoutHostWorkspaceStaysOpen(t *testing.T) {
	h := newWrapperHarness(t)
	h.sessions.answer("ag_a", "fertig\nSTATUS: DONE")
	out := h.runWrapper("alpha", "DOCKWEAVE_HOST_WORKSPACE=")
	if got := h.lastStatusUpdate(); got != "in_progress" {
		t.Fatalf("status without workspace = %q\n%s", got, out)
	}
	if !strings.Contains(h.lastComment(), "DOCKWEAVE_HOST_WORKSPACE") {
		t.Fatalf("missing explanation in issue comment: %q", h.lastComment())
	}
	if len(h.gateRuns()) != 0 || len(h.paperclipCalls("agent", "wake")) != 0 {
		t.Fatal("DONE without workspace ran gates or routed")
	}
}

// RF-028: a routing failure after a PASS keeps the verdict (issue done) and
// tells how to retry; the downstream agent is not woken.
func TestWrapperRoutingFailureKeepsDoneAndNamesRetry(t *testing.T) {
	h := newWrapperHarness(t)
	h.sessions.answer("ag_a", "fertig\nSTATUS: DONE")
	unmapped := filepath.Join(t.TempDir(), "map.json")
	if err := os.WriteFile(unmapped, []byte(`{"ag_a":"pc-a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := h.runWrapper("alpha", "DOCKWEAVE_PAPERCLIP_AGENT_MAP="+unmapped)
	if got := h.lastStatusUpdate(); got != "done" {
		t.Fatalf("routing failure discarded the PASS: status %q\n%s", got, out)
	}
	if c := h.lastComment(); !strings.Contains(c, "UNMAPPED") || !strings.Contains(c, "pipeline.py route --upstream alpha") {
		t.Fatalf("issue comment lacks failure and retry command: %q", c)
	}
	if len(h.paperclipCalls("agent", "wake")) != 0 {
		t.Fatal("unmapped downstream was woken")
	}
}

// RF-035: a gate that tampers with its own configuration is never a PASS,
// even through the wrapper; the follow-up agent stays blocked.
func TestWrapperTamperBlocksDownstream(t *testing.T) {
	h := newWrapperHarnessWithGate(t, `echo '{}' > .paperclip/gates.json`)
	h.sessions.answer("ag_a", "fertig\nSTATUS: DONE")
	out := h.runWrapper("alpha")
	runs := h.gateRuns()
	if len(runs) != 1 || !runs[0].Tamper || runs[0].Status == model.GatePassed {
		t.Fatalf("tamper run: %+v\n%s", runs, out)
	}
	if got := h.lastStatusUpdate(); got != "in_progress" {
		t.Fatalf("issue status after tamper = %q", got)
	}
	h.sessions.answer("ag_b", "B\nSTATUS: IN_PROGRESS")
	h.runWrapper("beta")
	if h.sessions.count("ag_b") != 0 || len(h.paperclipCalls("agent", "wake")) != 0 {
		t.Fatal("tamper released the downstream agent")
	}
}

// RF-035: a gate with a budget but no reachable broker fails closed through
// the wrapper: the budget check already fails the pre_run phase, so agent A
// gets no model turn at all, the issue is re-queued, nothing passes and the
// follow-up agent stays blocked.
func TestWrapperBudgetUnavailableBlocksDownstream(t *testing.T) {
	h := newWrapperHarnessWithConfig(t, `grep -qx ok ok.txt`, map[string]any{
		"budget": map[string]any{"grant_id": "g1", "max_requests_day": 20}})
	h.sessions.answer("ag_a", "fertig\nSTATUS: DONE")
	out := h.runWrapper("alpha")
	if h.sessions.count("ag_a") != 0 {
		t.Fatalf("agent A ran although the budget is unavailable\n%s", out)
	}
	if got := h.lastStatusUpdate(); got != "todo" || !strings.Contains(h.lastComment(), "pre_run") {
		t.Fatalf("issue not re-queued with pre_run reason: status %q comment %q", got, h.lastComment())
	}
	for _, r := range h.gateRuns() {
		if r.Status == model.GatePassed {
			t.Fatalf("budget without broker passed: %+v", r)
		}
	}
	h.sessions.answer("ag_b", "B\nSTATUS: IN_PROGRESS")
	h.runWrapper("beta")
	if h.sessions.count("ag_b") != 0 || len(h.paperclipCalls("agent", "wake")) != 0 {
		t.Fatal("budget failure released the downstream agent")
	}
}
