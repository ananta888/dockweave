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

func TestCreateGateReviewRequiresAdmin(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &Server{Store: store}

	call := func(role Role, name string, body string) (int, model.GateReview) {
		ctx := ContextWithPeer(context.Background(), name, 1000, role)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gates/reviews", strings.NewReader(body))
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		srv.createGateReview(rec, req)
		var out model.GateReview
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// A non-admin peer is rejected even when it claims to be the assignee.
	if code, _ := call(RoleMonitor, "bob", `{"issue_id":"DOC-2","verdict":"PASS","assigned_reviewer":"bob"}`); code != http.StatusForbidden {
		t.Fatalf("monitor review: code=%d, want 403", code)
	}
	if code, _ := call("", "nobody", `{"issue_id":"DOC-2","verdict":"PASS"}`); code != http.StatusForbidden {
		t.Fatalf("unauthenticated review: code=%d, want 403", code)
	}
	// An admin may record without an assignee.
	if code, out := call(RoleAdmin, "admin", `{"issue_id":"DOC-2","verdict":"PASS"}`); code != http.StatusOK || out.Actor != "admin" {
		t.Fatalf("admin review: code=%d %+v", code, out)
	}
	// The assignee claim is stored as metadata; the actor stays the peer.
	code, out := call(RoleAdmin, "admin", `{"issue_id":"DOC-3","verdict":"PASS","assigned_reviewer":"carol"}`)
	if code != http.StatusOK || out.AssignedReviewer != "carol" || out.Actor != "admin" {
		t.Fatalf("admin review with assignee: code=%d %+v", code, out)
	}
	stored, err := store.GetGateReview(context.Background(), "DOC-3")
	if err != nil || stored == nil || stored.AssignedReviewer != "carol" {
		t.Fatalf("assigned reviewer not persisted: %+v %v", stored, err)
	}
}

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
