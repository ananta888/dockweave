package sdk

import (
	"context"
	"encoding/json"
	"errors"
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
)

// RF-032: the SDK against the real daemon handlers and store (no mocked
// response shapes).
func realDaemon(t *testing.T) (*Client, string) {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC()
	for _, a := range []*model.Agent{
		{ID: "ag_a", Name: "alpha", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ag_b", Name: "beta", Type: "opencode", Image: "x", State: model.AgentRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	srv := api.NewServer(&agents.Service{Store: store}, nil, nil, store)
	srv.AllowedRoots = []string{filepath.Dir(ws)}
	srv.Pipeline = &pipeline.Evaluator{Store: store,
		Fingerprint: func(context.Context, string) (string, error) { return "sha256:x", nil }}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r.WithContext(api.ContextWithPeer(r.Context(), "sdk-test", 0, api.RoleAdmin)))
	}))
	t.Cleanup(hs.Close)
	return NewHTTP(hs.URL), ws
}

func edge(ws, id string) []model.PipelineEdge {
	return []model.PipelineEdge{{ID: id, GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta", Workspace: ws}}
}

func apiCode(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestSDKPipelineRevisionContract(t *testing.T) {
	c, ws := realDaemon(t)
	ctx := t.Context()
	st, err := c.PutPipelines(ctx, 3, false, edge(ws, "e1"))
	if err != nil || st.Revision != 3 || len(st.Edges) != 1 || st.Edges[0].UpstreamAgentID != "ag_a" {
		t.Fatalf("initial put: %+v %v", st, err)
	}
	if _, err := c.PutPipelines(ctx, 3, false, edge(ws, "e1")); err != nil {
		t.Fatalf("identical repeat must be idempotent: %v", err)
	}
	if _, err := c.PutPipelines(ctx, 3, false, edge(ws, "other")); apiCode(err) != "revision_conflict" {
		t.Fatalf("divergent same revision: %v", err)
	}
	if _, err := c.PutPipelines(ctx, 2, false, nil); apiCode(err) != "stale_revision" {
		t.Fatalf("older revision: %v", err)
	}
	if st, err := c.PutPipelines(ctx, 1, true, nil); err != nil || st.Revision != 1 || len(st.Edges) != 0 {
		t.Fatalf("reset to a lower revision: %+v %v", st, err)
	}
	if got, err := c.ListPipelines(ctx, "beta"); err != nil || got.Revision != 1 || len(got.Edges) != 0 {
		t.Fatalf("list after reset: %+v %v", got, err)
	}
}

func TestSDKAdmissionSingleAndCollectionShapes(t *testing.T) {
	c, ws := realDaemon(t)
	ctx := t.Context()
	if _, err := c.PutPipelines(ctx, 1, false, edge(ws, "e1")); err != nil {
		t.Fatal(err)
	}
	one, err := c.PipelineAdmission(ctx, "beta")
	if err != nil || one.AgentID != "ag_b" || one.Allowed || len(one.Edges) != 1 || one.Edges[0].Code != model.AdmitNoRun {
		t.Fatalf("single admission: %+v %v", one, err)
	}
	rev, all, err := c.PipelineAdmissions(ctx)
	if err != nil || rev != 1 || len(all) != 1 || all[0].AgentID != "ag_b" {
		t.Fatalf("collection: %d %+v %v", rev, all, err)
	}
	if _, err := c.PipelineAdmission(ctx, "ghost"); apiCode(err) != "not_found" {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestSDKOverrideTTLAndReuse(t *testing.T) {
	c, ws := realDaemon(t)
	ctx := t.Context()
	if _, err := c.PutPipelines(ctx, 1, false, edge(ws, "e1")); err != nil {
		t.Fatal(err)
	}
	reusable := false
	o, err := c.CreatePipelineOverride(ctx, OverrideRequest{Agent: "beta", GateID: "tests", Reason: "hotfix", TTLSec: 120, SingleUse: &reusable})
	if err != nil || o.SingleUse || o.ExpiresAt.Sub(o.CreatedAt) != 120*time.Second {
		t.Fatalf("override: %+v %v", o, err)
	}
	if _, err := c.CreatePipelineOverride(ctx, OverrideRequest{Agent: "beta", GateID: "tests", Reason: "x", TTLSec: 86401}); apiCode(err) != "invalid" {
		t.Fatalf("TTL above 24h: %v", err)
	}
	if _, err := c.CreatePipelineOverride(ctx, OverrideRequest{Agent: "beta", GateID: "tests"}); apiCode(err) != "invalid" {
		t.Fatalf("missing reason: %v", err)
	}
	active, err := c.ListPipelineOverrides(ctx, "beta", true)
	if err != nil || len(active) != 1 || active[0].ID != o.ID {
		t.Fatalf("active overrides: %+v %v", active, err)
	}
	// Reading the admission twice never consumes a reusable override.
	for i := 0; i < 2; i++ {
		if adm, err := c.PipelineAdmission(ctx, "beta"); err != nil || !adm.Allowed {
			t.Fatalf("admission %d with override: %+v %v", i, adm, err)
		}
	}
}

func TestSDKCancellationDuringRequest(t *testing.T) {
	started := make(chan struct{})
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer hs.Close()
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-started; cancel() }()
	done := make(chan error, 1)
	go func() { _, err := NewHTTP(hs.URL).PipelineAdmission(ctx, "beta"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop after cancellation")
	}
}

func TestSDKPipelineMethodsRejectMalformed200(t *testing.T) {
	for _, body := range []string{`<html>`, `{"revision":`, `"text"`} {
		hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		c := NewHTTP(hs.URL)
		ctx := t.Context()
		for name, call := range map[string]func() error{
			"put":        func() error { _, err := c.PutPipelines(ctx, 1, false, nil); return err },
			"list":       func() error { _, err := c.ListPipelines(ctx, ""); return err },
			"admission":  func() error { _, err := c.PipelineAdmission(ctx, "b"); return err },
			"admissions": func() error { _, _, err := c.PipelineAdmissions(ctx); return err },
			"override":   func() error { _, err := c.CreatePipelineOverride(ctx, OverrideRequest{}); return err },
			"overrides":  func() error { _, err := c.ListPipelineOverrides(ctx, "", false); return err },
		} {
			if err := call(); err == nil {
				t.Errorf("%s accepted malformed 200 body %q", name, body)
			}
		}
		hs.Close()
	}
}

func TestSDKSendsExplicitOptionalFields(t *testing.T) {
	var bodies []map[string]any
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		bodies = append(bodies, m)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	c := NewHTTP(hs.URL)
	off := false
	_, _ = c.CreatePipelineOverride(t.Context(), OverrideRequest{Agent: "b", GateID: "g", Reason: "r"})
	_, _ = c.CreatePipelineOverride(t.Context(), OverrideRequest{Agent: "b", GateID: "g", Reason: "r", TTLSec: 60, SingleUse: &off})
	_, _ = c.PutPipelines(t.Context(), 7, true, nil)
	if _, ok := bodies[0]["single_use"]; ok {
		t.Fatalf("unset single_use must be omitted (server default): %v", bodies[0])
	}
	if _, ok := bodies[0]["ttl_sec"]; ok {
		t.Fatalf("unset ttl must be omitted (server default): %v", bodies[0])
	}
	if bodies[1]["single_use"] != false || bodies[1]["ttl_sec"] != float64(60) {
		t.Fatalf("explicit values lost: %v", bodies[1])
	}
	if bodies[2]["revision"] != float64(7) || bodies[2]["reset"] != true {
		t.Fatalf("put fields: %v", bodies[2])
	}
	if edges, ok := bodies[2]["edges"].([]any); !ok || len(edges) != 0 {
		t.Fatalf("nil edges must be sent as [] (explicit clear): %v", bodies[2])
	}
}
