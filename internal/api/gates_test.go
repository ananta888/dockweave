package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

func TestCreateGateReviewIsIdempotentAndRejectsConflicts(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &Server{Store: store}
	ctx := ContextWithPeer(context.Background(), "tester", 1000, RoleAdmin)

	call := func(verdict string) (int, model.GateReview) {
		body := `{"issue_id":"DOC-1","verdict":"` + verdict + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gates/reviews", strings.NewReader(body))
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		srv.createGateReview(rec, req)
		var out model.GateReview
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, first := call("PASS")
	if code != http.StatusOK || first.Verdict != "PASS" || first.EvidenceID == "" {
		t.Fatalf("first review: code=%d %+v", code, first)
	}
	code, again := call("PASS")
	if code != http.StatusOK || again.EvidenceID != first.EvidenceID {
		t.Fatalf("idempotent review: code=%d %+v", code, again)
	}
	code, conflict := call("FAIL")
	if code != http.StatusConflict || !conflict.Conflict || conflict.Existing != "PASS" {
		t.Fatalf("conflict review: code=%d %+v", code, conflict)
	}
}
