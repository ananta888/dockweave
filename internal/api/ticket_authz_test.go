package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing/authz"
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
