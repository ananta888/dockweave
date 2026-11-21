package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
)

func putGraphReq(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.putGraph(rec, req)
	return rec
}

func TestPutGraphRevisionCheck(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := &Server{}

	if rec := putGraphReq(s, `{"nodes":[],"revision":0}`); rec.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", rec.Code, rec.Body)
	}
	if rec := putGraphReq(s, `{"nodes":[],"revision":0}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d, want 409", rec.Code)
	}
	if rec := putGraphReq(s, `{"nodes":[],"revision":7}`); rec.Code != http.StatusConflict {
		t.Fatalf("future revision: %d, want 409", rec.Code)
	}
	rec := putGraphReq(s, `{"nodes":[],"revision":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("current save: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Revision != 2 {
		t.Fatalf("revision = %d, want 2", out.Revision)
	}
}

func TestPutGraphLegacyFileWithoutRevision(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, _ := graphPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"nodes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	if rec := putGraphReq(s, `{"nodes":[],"revision":3}`); rec.Code != http.StatusConflict {
		t.Fatalf("revision mismatch on legacy file: %d, want 409", rec.Code)
	}
	if rec := putGraphReq(s, `{"nodes":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("legacy save at revision 0: %d %s", rec.Code, rec.Body)
	}
}

func TestPutGraphConcurrentSavesOneWins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := &Server{}
	if rec := putGraphReq(s, `{"revision":0}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	const n = 16
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = putGraphReq(s, `{"revision":1}`).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		} else if c != http.StatusConflict {
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent saves succeeded, want exactly 1", ok)
	}
}

func TestStampRevisionPreservesValues(t *testing.T) {
	out, err := stampRevision(json.RawMessage(`{"id":9007199254740993,"x":1.10,"revision":4}`), 5)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"id":9007199254740993`, `"x":1.10`, `"revision":5`} {
		if !strings.Contains(s, want) {
			t.Fatalf("stamped = %s, missing %s", s, want)
		}
	}
}

type fakePusher struct {
	mu    sync.Mutex
	calls []pushCall
	err   error
}

type pushCall struct {
	rev   int
	reset bool
	edges []model.PipelineEdge
}

func (f *fakePusher) push(_ context.Context, rev int, reset bool, edges []model.PipelineEdge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, pushCall{rev, reset, edges})
	return f.err
}

func pipelineGraph(t *testing.T, ws string, rev int, withEdge bool) string {
	t.Helper()
	conn := map[string]any{"id": "g1", "kind": "gate", "name": "Tests", "enabled": true, "agentId": "a",
		"contract": `{"gate_id":"tests","command":"npm test","on_false":"rework"}`}
	if withEdge {
		conn["targetAgentId"] = "b"
	}
	b, _ := json.Marshal(map[string]any{"revision": rev, "connectors": []any{conn},
		"paperclip": map[string]any{"workspace": ws, "syncGates": false}})
	return string(b)
}

func TestPutGraphPushesPipelineEdges(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	fp := &fakePusher{}
	s := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}

	rec := putGraphReq(s, pipelineGraph(t, ws, 0, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		PipelineSync PipelineSync `json:"pipeline_sync"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.PipelineSync.Pushed || out.PipelineSync.Edges != 1 || out.PipelineSync.Error != "" {
		t.Fatalf("pipeline_sync = %+v", out.PipelineSync)
	}
	if len(fp.calls) != 1 || fp.calls[0].rev != 1 || !fp.calls[0].reset || fp.calls[0].edges[0].GateID != "tests" ||
		fp.calls[0].edges[0].DownstreamAgentID != "b" || fp.calls[0].edges[0].OnFalse != "rework" {
		t.Fatalf("push = %+v (first save of a new graph file must reset)", fp.calls)
	}

	// Removing the target agent pushes an empty edge set (the daemon drops it).
	if rec := putGraphReq(s, pipelineGraph(t, ws, 1, false)); rec.Code != http.StatusOK {
		t.Fatalf("second save: %d", rec.Code)
	}
	if len(fp.calls) != 2 || fp.calls[1].rev != 2 || fp.calls[1].reset || len(fp.calls[1].edges) != 0 {
		t.Fatalf("second push = %+v", fp.calls[1])
	}
}

func TestPutGraphPipelineErrorKeepsGraph(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	fp := &fakePusher{err: errors.New("forbidden: only admins may change pipelines")}
	s := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}
	rec := putGraphReq(s, pipelineGraph(t, ws, 0, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("a pipeline push error must not fail the save: %d", rec.Code)
	}
	var out struct {
		Saved        bool         `json:"saved"`
		PipelineSync PipelineSync `json:"pipeline_sync"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Saved || out.PipelineSync.Pushed || !strings.Contains(out.PipelineSync.Error, "only admins") {
		t.Fatalf("response = %+v", out)
	}
	path, _ := graphPath()
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), "targetAgentId") {
		t.Fatalf("graph not saved: %v", err)
	}

	// Without a daemon, edges are reported as not enforced.
	s2 := &Server{browseRoots: []string{ws}}
	got := s2.syncPipeline(context.Background(), []byte(pipelineGraph(t, ws, 0, true)), 1, false)
	if got.Pushed || !strings.Contains(got.Error, "not enforced") {
		t.Fatalf("no daemon: %+v", got)
	}
}

func TestValidateExpressionEndpoint(t *testing.T) {
	s := &Server{}
	call := func(body string) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/graph/validate-expression", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.validateExpression(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", body, rec.Code)
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if out := call(`{"expression":"command == 0 && budget < 5"}`); out["ok"] != true || out["uses_budget"] != true || out["warning"] == nil {
		t.Fatalf("budget without config: %v", out)
	}
	if out := call(`{"expression":"budget < 5","budget_configured":true}`); out["ok"] != true || out["warning"] != nil {
		t.Fatalf("budget with config: %v", out)
	}
	if out := call(`{"expression":"true"}`); out["ok"] != true || out["uses_budget"] != false {
		t.Fatalf("true: %v", out)
	}
	if out := call(`{"expression":""}`); out["ok"] != true {
		t.Fatalf("empty: %v", out)
	}
	if out := call(`{"expression":"command =="}`); out["ok"] != false || out["error"] == nil {
		t.Fatalf("invalid: %v", out)
	}
	req := httptest.NewRequest(http.MethodPost, "/graph/validate-expression", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	s.validateExpression(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}
