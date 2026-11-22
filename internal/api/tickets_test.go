package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/memory"
	"github.com/ananta888/dockweave/internal/ticketing/native"
)

func providerStatus(t *testing.T, f *pipelineFixture) map[string]any {
	t.Helper()
	rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/tickets/provider", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("provider as monitor: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTicketProviderNotConfiguredSaysSo(t *testing.T) {
	f := newPipelineFixture(t)
	out := providerStatus(t, f)
	if out["configured"] != false || !strings.Contains(out["error"].(string), "TICKET_PROVIDER") || out["primary"] != nil {
		t.Fatalf("unconfigured: %+v", out)
	}
}

func TestTicketProviderShowsPrimaryMirrorHealthAndCapabilities(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Tickets = &ticketing.Selection{Primary: memory.New("native"), Mirror: memory.New("paperclip")}
	out := providerStatus(t, f)
	p := out["primary"].(map[string]any)
	m := out["mirror"].(map[string]any)
	if out["configured"] != true || p["name"] != "native" || m["name"] != "paperclip" ||
		p["health"].(map[string]any)["ok"] != true || p["capabilities"].(map[string]any)["threads"] != true {
		t.Fatalf("configured: %+v", out)
	}
}

func TestTicketProviderOpenErrorIsVisible(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.TicketsError = `open ticket provider "paperclip": boom`
	if out := providerStatus(t, f); out["configured"] != false || out["error"] != f.srv.TicketsError {
		t.Fatalf("open error: %+v", out)
	}
}

func ticketFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	f := newPipelineFixture(t)
	f.srv.Tickets = &ticketing.Selection{Primary: ticketing.Observe(native.New(f.store), NewTicketAudit(f.store))}
	return f
}

func decodeTicket(t *testing.T, body []byte) ticketing.Ticket {
	t.Helper()
	var tk ticketing.Ticket
	if err := json.Unmarshal(body, &tk); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return tk
}

func TestTicketAPIUnconfiguredIs503(t *testing.T) {
	f := newPipelineFixture(t)
	rec := f.do(t, RoleAdmin, http.MethodGet, "/api/v1/tickets", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "ticketing_unavailable") {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body)
	}
}

func TestTicketAPILifecycleWithAudit(t *testing.T) {
	f := ticketFixture(t)
	// Create with the agent given by name; the API stores the stable id.
	rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets", `{"title":"Fix login","priority":"high","assignee":{"kind":"agent","id":"beta"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	tk := decodeTicket(t, rec.Body.Bytes())
	if tk.Assignee == nil || tk.Assignee.ID != "ag_b" || tk.Identifier == "" || tk.CreatedBy == nil || !strings.HasPrefix(tk.CreatedBy.ID, "peer:") {
		t.Fatalf("created: %+v", tk)
	}
	id := tk.Ref.ID
	// Get by bare id and by provider ref; another provider's ref is 404.
	for _, path := range []string{"/api/v1/tickets/" + id, "/api/v1/tickets/native:" + id} {
		if rec := f.do(t, RoleMonitor, http.MethodGet, path, ""); rec.Code != http.StatusOK {
			t.Fatalf("get %s: %d", path, rec.Code)
		}
	}
	if rec := f.do(t, RoleAdmin, http.MethodGet, "/api/v1/tickets/paperclip:"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign ref: %d", rec.Code)
	}
	// Through the real peer middleware a monitor may read but not write.
	h := PeerAuthMiddleware(NewAuthPolicy([]uint32{0}, []uint32{2000}), f.srv.mux)
	for method, want := range map[string]int{http.MethodGet: http.StatusOK, http.MethodPatch: http.StatusForbidden} {
		req := httptest.NewRequest(method, "/api/v1/tickets/"+id, strings.NewReader(`{"title":"x"}`))
		req = req.WithContext(ContextWithPeer(req.Context(), "monitor", 2000, ""))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Fatalf("monitor %s: %d, want %d", method, rr.Code, want)
		}
	}
	// Optimistic update: the stale version is a conflict.
	rec = f.do(t, RoleAdmin, http.MethodPatch, "/api/v1/tickets/"+id, `{"status":"in_progress","expected_version":"`+tk.Version+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPatch, "/api/v1/tickets/"+id, `{"status":"done","expected_version":"`+tk.Version+`"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale update: %d %s", rec.Code, rec.Body)
	}
	// Unknown fields and bad values are 400, not silently ignored.
	for _, body := range []string{`{"titel":"typo"}`, `{"status":"weird"}`, `{"assignee":{"kind":"agent","id":"ghost"}}`} {
		if rec := f.do(t, RoleAdmin, http.MethodPatch, "/api/v1/tickets/"+id, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// Thread.
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/"+id+"/comments", `{"body":"on it"}`); rec.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body)
	}
	rec = f.do(t, RoleMonitor, http.MethodGet, "/api/v1/tickets/"+id+"/comments", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "on it") {
		t.Fatalf("comments: %d %s", rec.Code, rec.Body)
	}
	// Checkout by name; a second agent conflicts; release by the holder.
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/"+id+"/checkout", `{"agent":"beta"}`); rec.Code != http.StatusOK ||
		decodeTicket(t, rec.Body.Bytes()).CheckedOutBy != "ag_b" {
		t.Fatalf("checkout: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/"+id+"/checkout", `{"agent":"alpha"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second checkout: %d", rec.Code)
	}
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/"+id+"/release", `{"agent":"ag_b"}`); rec.Code != http.StatusOK {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	// List with filters.
	rec = f.do(t, RoleMonitor, http.MethodGet, "/api/v1/tickets?status=in_progress&assignee=agent:beta&q=login&limit=5", "")
	var list struct {
		Provider string             `json:"provider"`
		Tickets  []ticketing.Ticket `json:"tickets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Provider != "native" || len(list.Tickets) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"assignee=nobody", "updated_since=yesterday", "limit=-1"} {
		if rec := f.do(t, RoleMonitor, http.MethodGet, "/api/v1/tickets?"+q, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}
	if rec := f.do(t, RoleAdmin, http.MethodGet, "/api/v1/tickets/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rec.Code)
	}
	// Every mutation is in the audit chain with its actor.
	evs, err := f.store.ListEvents(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range evs {
		if strings.HasPrefix(string(e.Type), "ticket.") {
			seen[string(e.Type)]++
		}
	}
	for _, typ := range []string{ticketing.EventCreated, ticketing.EventUpdated, ticketing.EventStatusChanged, ticketing.EventCommentAdded, ticketing.EventCheckedOut, ticketing.EventReleased} {
		if seen[typ] != 1 {
			t.Fatalf("audit %s = %d (all: %v)", typ, seen[typ], seen)
		}
	}
}
