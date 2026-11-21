package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphRejectsNonObjectsAndInvalidRevisions(t *testing.T) {
	for _, body := range []string{"null", "[]", `"graph"`, "1", "{} {}", `{"revision":null}`, `{"revision":1.5}`, `{"revision":-1}`, `{"revision":"1"}`} {
		t.Run(body, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("input panicked: %v", v)
				}
			}()
			rec := putGraphReq(&Server{}, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestGraphSyncFailureSurvivesReloadAndCanRetryCurrentRevision(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	fp := &fakePusher{err: errors.New("daemon unavailable")}
	s := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}
	if rec := putGraphReq(s, pipelineGraph(t, ws, 0, true)); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	reloaded := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}
	rec := httptest.NewRecorder()
	reloaded.getGraph(rec, httptest.NewRequest(http.MethodGet, "/graph", nil))
	var got struct {
		Pipeline PipelineSync      `json:"pipeline_sync"`
		Bindings map[string]string `json:"pipeline_bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Pipeline.Error == "" || got.Bindings["g1"] == "" {
		t.Fatalf("reload lost synchronization failure/identity: %s", rec.Body)
	}
	fp.err = nil
	rec = httptest.NewRecorder()
	reloaded.retryGraphSync(rec, httptest.NewRequest(http.MethodPost, "/graph/sync", strings.NewReader(`{"revision":1}`)))
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	got.Pipeline = PipelineSync{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Pipeline.Pushed || got.Pipeline.Error != "" || fp.calls[len(fp.calls)-1].reset {
		t.Fatalf("retry failed or reset: %s", rec.Body)
	}
	count := len(fp.calls)
	rec = httptest.NewRecorder()
	reloaded.retryGraphSync(rec, httptest.NewRequest(http.MethodPost, "/graph/sync", strings.NewReader(`{"revision":0}`)))
	if rec.Code != 409 || len(fp.calls) != count {
		t.Fatalf("stale retry changed pipeline: %d", rec.Code)
	}
}

func TestGateFileFailureCannotPublishPipelineReplacement(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".paperclip"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &fakePusher{}
	s := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}
	raw := strings.Replace(pipelineGraph(t, ws, 0, true), `"syncGates":false`, `"syncGates":true`, 1)
	rec := putGraphReq(s, raw)
	if rec.Code != 200 || len(fp.calls) != 0 {
		t.Fatalf("partial gate sync published edges: %d, %+v", rec.Code, fp.calls)
	}
	var out struct {
		Pipeline PipelineSync `json:"pipeline_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Pipeline.Error == "" {
		t.Fatal("missing partial-sync error")
	}
}

func TestInvalidContractPreservesEnforcedPipeline(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()
	fp := &fakePusher{}
	s := &Server{browseRoots: []string{ws}, pushPipeline: fp.push}
	if rec := putGraphReq(s, pipelineGraph(t, ws, 0, true)); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(pipelineGraph(t, ws, 1, true)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["connectors"] = json.RawMessage(`[{"id":"g1","kind":"gate","enabled":true,"agentId":"a","targetAgentId":"b","contract":"{"}]`)
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	rec := putGraphReq(s, string(body))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if len(fp.calls) != 1 {
		t.Fatalf("invalid graph replaced enforced edges: %+v", fp.calls)
	}
	var out struct {
		PipelineSync PipelineSync `json:"pipeline_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.PipelineSync.Error == "" {
		t.Fatal("missing sync error")
	}
	path, _ := graphPath()
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
}
