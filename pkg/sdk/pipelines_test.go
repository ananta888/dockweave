package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
)

func TestPipelineMethods(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/pipelines/admission":
			if r.URL.Query().Get("agent") == "" {
				_, _ = w.Write([]byte(`{"revision":3,"admissions":[{"agent_id":"b","allowed":false}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"agent_id":"b","allowed":true,"action":"allow","edges":[]}`))
		case "/api/v1/pipelines/override":
			_, _ = w.Write([]byte(`{"id":"po-1","agent_id":"b","gate_id":"tests","single_use":true}`))
		case "/api/v1/pipelines/overrides":
			_, _ = w.Write([]byte(`{"overrides":[{"id":"po-1"}]}`))
		default:
			_, _ = w.Write([]byte(`{"revision":2,"edges":[{"id":"e","gate_id":"tests"}]}`))
		}
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL)
	ctx := context.Background()

	st, err := c.PutPipelines(ctx, 2, false, nil)
	if err != nil || st.Revision != 2 || gotMethod != "PUT" {
		t.Fatalf("PutPipelines = %+v %v %s", st, err, gotMethod)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(gotBody), &body)
	if edges, ok := body["edges"].([]any); !ok || len(edges) != 0 {
		t.Fatalf("nil edges must be sent as [] (clears the daemon): %s", gotBody)
	}
	if _, err := c.ListPipelines(ctx, "be ta"); err != nil || gotPath != "/api/v1/pipelines?agent=be+ta" {
		t.Fatalf("ListPipelines path %s %v", gotPath, err)
	}
	adm, err := c.PipelineAdmission(ctx, "b")
	if err != nil || !adm.Allowed {
		t.Fatalf("admission = %+v %v", adm, err)
	}
	rev, all, err := c.PipelineAdmissions(ctx)
	if err != nil || rev != 3 || len(all) != 1 {
		t.Fatalf("admissions = %d %v %v", rev, all, err)
	}
	single := false
	o, err := c.CreatePipelineOverride(ctx, OverrideRequest{Agent: "b", GateID: "tests", Reason: "x", SingleUse: &single})
	if err != nil || o.ID != "po-1" || gotMethod != "POST" {
		t.Fatalf("override = %+v %v", o, err)
	}
	list, err := c.ListPipelineOverrides(ctx, "b", true)
	if err != nil || len(list) != 1 || gotPath != "/api/v1/pipelines/overrides?active=true&agent=b" {
		t.Fatalf("overrides = %v %v %s", list, err, gotPath)
	}
	_ = model.AdmitOK
}
