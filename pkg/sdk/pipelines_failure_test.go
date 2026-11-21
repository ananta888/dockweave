package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPipelineErrorsPreserveStructuredCode(t *testing.T) {
	for _, status := range []int{403, 404, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"code":"test_error","message":"details"}}`))
			}))
			defer server.Close()
			c := NewHTTP(server.URL)
			calls := []func() error{
				func() error { _, err := c.PutPipelines(t.Context(), 1, false, nil); return err },
				func() error { _, err := c.ListPipelines(t.Context(), ""); return err },
				func() error { _, err := c.PipelineAdmission(t.Context(), "b"); return err },
				func() error { _, _, err := c.PipelineAdmissions(t.Context()); return err },
				func() error { _, err := c.CreatePipelineOverride(t.Context(), OverrideRequest{}); return err },
				func() error { _, err := c.ListPipelineOverrides(t.Context(), "", false); return err },
			}
			for _, call := range calls {
				var apiErr *APIError
				if err := call(); !errors.As(err, &apiErr) || apiErr.Code != "test_error" || apiErr.Message != "details" {
					t.Fatalf("structured error lost: %v", err)
				}
			}
		})
	}
}

func TestPipelineAdmissionRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"allowed":`))
	}))
	defer server.Close()
	if _, err := NewHTTP(server.URL).PipelineAdmission(t.Context(), "b"); err == nil {
		t.Fatal("malformed response accepted")
	}
}

func TestPipelineAdmissionRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewHTTP("http://127.0.0.1:1").PipelineAdmission(ctx, "b")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPipelineOverridePreservesFalseAndEscapesAgent(t *testing.T) {
	const agent = "team/a & b?x=1+#ä"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var req struct {
				SingleUse *bool `json:"single_use"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SingleUse == nil || *req.SingleUse {
				t.Errorf("explicit false was lost: %+v %v", req, err)
			}
			_, _ = w.Write([]byte(`{"id":"o","single_use":false}`))
			return
		}
		if r.URL.Query().Get("agent") != agent || r.URL.Query().Get("active") != "true" || len(r.URL.Query()) != 2 {
			t.Errorf("query not escaped: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"overrides":[]}`))
	}))
	defer server.Close()
	c := NewHTTP(server.URL)
	single := false
	if _, err := c.CreatePipelineOverride(t.Context(), OverrideRequest{SingleUse: &single}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListPipelineOverrides(t.Context(), agent, true); err != nil {
		t.Fatal(err)
	}
}
