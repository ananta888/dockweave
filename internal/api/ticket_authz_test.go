package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/dispatch"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/authz"
	"github.com/ananta888/dockweave/internal/workspaces"
)

// asHuman calls the API as a human forwarded by the gateway with groups.
func asHuman(t *testing.T, f *pipelineFixture, groups []string, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := ContextWithPeer(req.Context(), "gateway", 1001, RoleAdmin)
	req = req.WithContext(ContextWithHuman(ctx, model.HumanActor{ID: "iss|" + strings.Join(groups, "+"), Groups: groups}))
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func TestTicketPermissionsPerRole(t *testing.T) {
	f := ticketFixture(t)
	path := filepath.Join(t.TempDir(), "ticket-roles.json")
	cfg := `{"groups":{"dw-admin":["admin"],"dw-author":["author"],"dw-approver":["approver"],"dw-reader":["reader"]},
	 "roles":{"admin":["*"],"author":["ticket.read","ticket.create","ticket.edit","ticket.comment"],
	  "approver":["ticket.read","ticket.comment","ticket.assign","gate.override"],"reader":["ticket.read"]}}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	f.srv.TicketRoles = &authz.Store{Path: path, CheckEvery: time.Nanosecond}

	author, approver, reader, stranger := []string{"dw-author"}, []string{"dw-approver"}, []string{"dw-reader"}, []string{"other"}
	rec := asHuman(t, f, author, http.MethodPost, "/api/v1/tickets", `{"title":"x"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("author create: %d %s", rec.Code, rec.Body)
	}
	tk := decodeTicket(t, rec.Body.Bytes())
	one := "/api/v1/tickets/" + tk.Ref.ID
	cases := []struct {
		who          []string
		method, path string
		body         string
		want         int
		perm         string
	}{
		{author, "POST", "/api/v1/tickets", `{"title":"y","assignee":{"kind":"agent","id":"beta"}}`, 403, "ticket.assign"},
		{author, "PATCH", one, `{"title":"z"}`, 200, ""},
		{author, "PATCH", one, `{"assignee":{"kind":"agent","id":"beta"}}`, 403, "ticket.assign"},
		{author, "POST", one + "/comments", `{"body":"hi"}`, 201, ""},
		{author, "POST", one + "/checkout", `{"agent":"beta"}`, 403, "ticket.assign"},
		{approver, "PATCH", one, `{"assignee":{"kind":"agent","id":"beta"}}`, 200, ""},
		{approver, "PATCH", one, `{"title":"nope"}`, 403, "ticket.edit"},
		{approver, "POST", one + "/checkout", `{"agent":"beta"}`, 200, ""},
		{reader, "GET", one, "", 200, ""},
		{reader, "GET", "/api/v1/tickets", "", 200, ""},
		{reader, "POST", one + "/comments", `{"body":"hi"}`, 403, "ticket.comment"},
		{reader, "POST", "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix"}`, 403, "gate.override"},
		{stranger, "GET", one, "", 403, "ticket.read"},
		{stranger, "GET", "/api/v1/tickets/provider", "", 403, "ticket.read"},
	}
	for _, c := range cases {
		rec := asHuman(t, f, c.who, c.method, c.path, c.body)
		if rec.Code != c.want {
			t.Fatalf("%v %s %s: %d, want %d: %s", c.who, c.method, c.path, rec.Code, c.want, rec.Body)
		}
		if c.perm != "" && !strings.Contains(rec.Body.String(), `"permission":"`+c.perm+`"`) {
			t.Fatalf("%v %s %s: missing permission name %s: %s", c.who, c.method, c.path, c.perm, rec.Body)
		}
	}

	// /tickets/me lists roles and permissions for the UI.
	rec = asHuman(t, f, author, "GET", "/api/v1/tickets/me", "")
	var me struct {
		Roles       []string `json:"roles"`
		Permissions []string `json:"permissions"`
		Source      string   `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Source != "groups" || len(me.Roles) != 1 || me.Roles[0] != "author" || len(me.Permissions) != 4 {
		t.Fatalf("me: %s", rec.Body)
	}

	// A broken file makes humans read-only immediately and is reported.
	if err := os.WriteFile(path, []byte(`{"roles":{"author":["ticket.raed"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, time.Now(), time.Now().Add(time.Hour))
	if rec := asHuman(t, f, author, "PATCH", one, `{"title":"after"}`); rec.Code != 403 {
		t.Fatalf("invalid config still grants edit: %d", rec.Code)
	}
	if rec := asHuman(t, f, author, "GET", "/api/v1/tickets/me", ""); !strings.Contains(rec.Body.String(), "read-only") {
		t.Fatalf("config error not reported: %s", rec.Body)
	}

	// The CLI peer (no forwarded human) keeps its peer role.
	if rec := f.do(t, RoleAdmin, "PATCH", one, `{"title":"cli"}`); rec.Code != 200 {
		t.Fatalf("peer admin: %d %s", rec.Code, rec.Body)
	}
}

// Approvals need ticket.approve for humans, record the human as actor and
// let a parent waiting for the approval continue (TK-014).
func TestApprovalPermissionActorAndContinuation(t *testing.T) {
	f := ticketFixture(t)
	f.srv.Dispatcher = f.srv.NewDispatcher(DispatchSettings{}, f.srv.Tickets.Primary, AgentWaker{Server: f.srv, Scheduler: &dispatch.Scheduler{}})
	tk, err := f.srv.Tickets.Primary.Tickets().Create(context.Background(), ticketing.NewTicket{Title: "Goal", Status: ticketing.StatusInReview,
		Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_a"}}, ticketing.Actor{Kind: ticketing.ActorSystem})
	if err != nil {
		t.Fatal(err)
	}
	key := dispatch.IssueKey(tk.Ref)
	body := `{"issue_id":"` + key + `"}`
	if rec := asHuman(t, f, []string{"dockweave-monitor"}, http.MethodPost, "/api/v1/gates/approvals", body); rec.Code != http.StatusForbidden {
		t.Fatalf("reader approve: %d", rec.Code)
	}
	rec := asHuman(t, f, []string{"dockweave-admin"}, http.MethodPost, "/api/v1/gates/approvals", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"actor":"iss|dockweave-admin"`) {
		t.Fatalf("admin approve: %d %s", rec.Code, rec.Body)
	}
	got, _ := f.srv.Tickets.Primary.Tickets().Get(context.Background(), tk.Ref.ID)
	if got.Status != ticketing.StatusInProgress {
		t.Fatalf("parent did not continue: %s", got.Status)
	}
}

// Delete needs ticket.delete, refuses a parent with children and is
// audited; known humans come from forwarded requests; a workspace must
// exist and is stored by id.
func TestTicketDeleteHumansAndWorkspace(t *testing.T) {
	f := ticketFixture(t)
	ctx := context.Background()
	parent := decodeTicket(t, f.do(t, RoleAdmin, "POST", "/api/v1/tickets", `{"title":"parent"}`).Body.Bytes())
	child := decodeTicket(t, f.do(t, RoleAdmin, "POST", "/api/v1/tickets", `{"title":"child","parent":{"provider":"native","id":"`+parent.Ref.ID+`"}}`).Body.Bytes())
	if rec := asHuman(t, f, []string{"dockweave-monitor"}, "DELETE", "/api/v1/tickets/"+child.Ref.ID, ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "ticket.delete") {
		t.Fatalf("reader delete: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleAdmin, "DELETE", "/api/v1/tickets/"+parent.Ref.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete parent with child: %d", rec.Code)
	}
	if rec := asHuman(t, f, []string{"dockweave-admin"}, "DELETE", "/api/v1/tickets/"+child.Ref.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, RoleAdmin, "GET", "/api/v1/tickets/"+child.Ref.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted ticket readable: %d", rec.Code)
	}
	evs, _ := f.store.ListEvents(ctx, 100)
	found := false
	for _, e := range evs {
		found = found || (string(e.Type) == "ticket.deleted" && strings.Contains(e.Message, "child"))
	}
	if !found {
		t.Fatal("delete not audited")
	}

	// Known humans: recorded through the full server (not only the mux).
	req := httptest.NewRequest("GET", "/api/v1/tickets", nil)
	req = req.WithContext(ContextWithHuman(ContextWithPeer(req.Context(), "gateway", 1001, RoleAdmin),
		model.HumanActor{ID: "iss|bob", Name: "bob", Groups: []string{"dockweave-admin"}}))
	f.srv.ServeHTTP(httptest.NewRecorder(), req)
	rec := f.do(t, RoleMonitor, "GET", "/api/v1/tickets/humans", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"iss|bob"`) || !strings.Contains(rec.Body.String(), `"name":"bob"`) {
		t.Fatalf("humans: %d %s", rec.Code, rec.Body)
	}
	// A human can be assigned.
	rec = f.do(t, RoleAdmin, "PATCH", "/api/v1/tickets/"+parent.Ref.ID, `{"assignee":{"kind":"human","id":"iss|bob","name":"bob"}}`)
	if rec.Code != 200 || decodeTicket(t, rec.Body.Bytes()).Assignee.ID != "iss|bob" {
		t.Fatalf("human assignee: %d %s", rec.Code, rec.Body)
	}

	// Workspaces: unknown → 400; by name → stored as id.
	f.srv.Workspaces = &workspaces.Service{Store: f.store}
	if err := f.store.CreateWorkspace(ctx, &model.Workspace{ID: "ws_1", Name: "proj", HostPath: f.ws, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, RoleAdmin, "PATCH", "/api/v1/tickets/"+parent.Ref.ID, `{"workspace_id":"ghost"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown workspace: %d", rec.Code)
	}
	rec = f.do(t, RoleAdmin, "PATCH", "/api/v1/tickets/"+parent.Ref.ID, `{"workspace_id":"proj"}`)
	if rec.Code != 200 || decodeTicket(t, rec.Body.Bytes()).WorkspaceID != "ws_1" {
		t.Fatalf("workspace by name: %d %s", rec.Code, rec.Body)
	}
}
