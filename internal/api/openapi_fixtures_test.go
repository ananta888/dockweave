package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/memory"
	"github.com/ananta888/dockweave/internal/ticketing/native"
	"github.com/ananta888/dockweave/internal/ticketing/tsync"
)

// TestRecordPipelineResponsesForOpenAPI records real handler responses for
// the pipeline API (RF-022). scripts/validate_openapi.py runs it with
// DOCKWEAVE_OPENAPI_FIXTURES=<dir> and validates every response against
// docs/api/openapi.yaml. Without the variable it only checks that every
// scenario produced the expected status.
func TestRecordPipelineResponsesForOpenAPI(t *testing.T) {
	f := newPipelineFixture(t)
	f.srv.Sessions = &fakeSessions{out: "x"}
	type fixture struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	var out []fixture
	rec := func(role Role, method, path, template, body string, want int) {
		t.Helper()
		r := f.do(t, role, method, path, body)
		if r.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, r.Code, want, r.Body)
		}
		out = append(out, fixture{method, template, r.Code, json.RawMessage(r.Body.Bytes())})
	}
	const adm = "/api/v1/pipelines/admission"
	rec(RoleAdmin, "GET", adm, adm, "", 200) // no edges yet: empty collection
	rec(RoleAdmin, "GET", "/api/v1/pipelines", "/api/v1/pipelines", "", 200)
	if r := f.pushEdges(t, 2); r.Code != 200 {
		t.Fatal(r.Body)
	}
	out = append(out, fixture{"PUT", "/api/v1/pipelines", 200, json.RawMessage(f.pushEdges(t, 2).Body.Bytes())})
	rec(RoleAdmin, "PUT", "/api/v1/pipelines", "/api/v1/pipelines", `{"revision":1,"edges":[]}`, 409)
	rec(RoleAdmin, "PUT", "/api/v1/pipelines", "/api/v1/pipelines", `{"revision":2,"edges":[]}`, 409)
	rec(RoleAdmin, "PUT", "/api/v1/pipelines", "/api/v1/pipelines", `{"revision":-1,"edges":[]}`, 400)
	rec(RoleMonitor, "PUT", "/api/v1/pipelines", "/api/v1/pipelines", `{"revision":3,"edges":[]}`, 403)
	rec(RoleAdmin, "GET", "/api/v1/pipelines?agent=beta", "/api/v1/pipelines", "", 200)
	rec(RoleAdmin, "GET", adm+"?agent=beta", adm, "", 200)
	rec(RoleAdmin, "GET", adm, adm, "", 200)
	rec(RoleAdmin, "GET", adm+"?agent=ghost", adm, "", 404)
	rec(RoleAdmin, "GET", "/api/v1/agents/beta/pipeline", "/api/v1/agents/{id}/pipeline", "", 200)
	rec(RoleAdmin, "POST", "/v1/chat/completions", "/v1/chat/completions", chatBody("beta"), 409)
	rec(RoleAdmin, "POST", "/api/v1/pipelines/override", "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests"}`, 400)
	rec(RoleAdmin, "POST", "/api/v1/pipelines/override", "/api/v1/pipelines/override", `{"agent":"ghost","gate_id":"tests","reason":"why not"}`, 404)
	rec(RoleMonitor, "POST", "/api/v1/pipelines/override", "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"why not"}`, 403)
	rec(RoleAdmin, "POST", "/api/v1/pipelines/override", "/api/v1/pipelines/override", `{"agent":"beta","gate_id":"tests","reason":"hotfix"}`, 200)
	rec(RoleAdmin, "GET", "/api/v1/pipelines/overrides?agent=beta&active=true", "/api/v1/pipelines/overrides", "", 200)
	f.saveRun(t, "r-pass", model.GatePassed, "sha256:one")
	rec(RoleAdmin, "GET", adm+"?agent=beta", adm, "", 200)

	rec(RoleMonitor, "GET", "/api/v1/live", "/api/v1/live", "", 200)
	rec(RoleMonitor, "GET", "/api/v1/tickets/provider", "/api/v1/tickets/provider", "", 200)
	f.srv.Tickets = &ticketing.Selection{Primary: memory.New("native"), Mirror: memory.New("paperclip")}
	rec(RoleMonitor, "GET", "/api/v1/tickets/provider", "/api/v1/tickets/provider", "", 200)
	f.srv.Tickets = nil
	rec(RoleAdmin, "GET", "/api/v1/tickets", "/api/v1/tickets", "", 503)
	f.srv.Tickets = &ticketing.Selection{Primary: ticketing.Observe(native.New(f.store), NewTicketAudit(f.store))}
	created := f.do(t, RoleAdmin, "POST", "/api/v1/tickets", `{"title":"Fix login","priority":"high","assignee":{"kind":"agent","id":"beta"}}`)
	if created.Code != 201 {
		t.Fatal(created.Body)
	}
	out = append(out, fixture{"POST", "/api/v1/tickets", 201, json.RawMessage(created.Body.Bytes())})
	var tk ticketing.Ticket
	_ = json.Unmarshal(created.Body.Bytes(), &tk)
	one := "/api/v1/tickets/" + tk.Ref.ID
	rec(RoleAdmin, "POST", "/api/v1/tickets", "/api/v1/tickets", `{"title":""}`, 400)
	rec(RoleMonitor, "GET", "/api/v1/tickets?status=todo", "/api/v1/tickets", "", 200)
	rec(RoleMonitor, "GET", one, "/api/v1/tickets/{id}", "", 200)
	rec(RoleMonitor, "GET", "/api/v1/tickets/nope", "/api/v1/tickets/{id}", "", 404)
	rec(RoleAdmin, "PATCH", one, "/api/v1/tickets/{id}", `{"status":"in_progress"}`, 200)
	rec(RoleAdmin, "PATCH", one, "/api/v1/tickets/{id}", `{"status":"done","expected_version":"1"}`, 409)
	rec(RoleAdmin, "POST", one+"/comments", "/api/v1/tickets/{id}/comments", `{"body":"on it"}`, 201)
	rec(RoleMonitor, "GET", one+"/comments", "/api/v1/tickets/{id}/comments", "", 200)
	rec(RoleAdmin, "POST", one+"/checkout", "/api/v1/tickets/{id}/checkout", `{"agent":"beta"}`, 200)
	rec(RoleAdmin, "POST", one+"/checkout", "/api/v1/tickets/{id}/checkout", `{"agent":"alpha"}`, 409)
	rec(RoleAdmin, "POST", one+"/release", "/api/v1/tickets/{id}/release", `{"agent":"beta"}`, 200)
	rec(RoleMonitor, "GET", "/api/v1/tickets/me", "/api/v1/tickets/me", "", 200)
	f.srv.Dispatcher = f.srv.NewDispatcher(DispatchSettings{}, f.srv.Tickets.Primary, nil)
	rec(RoleAdmin, "POST", "/api/v1/tickets/run", "/api/v1/tickets/run", `{"agent":"alpha"}`, 200)
	rec(RoleAdmin, "POST", "/api/v1/tickets/run", "/api/v1/tickets/run", `{"agent":"ghost"}`, 400)
	rec(RoleAdmin, "POST", one+"/route", "/api/v1/tickets/{id}/route", `{"agent":"alpha"}`, 200)
	f.srv.Dispatcher = nil
	rec(RoleMonitor, "GET", "/api/v1/tickets/sync", "/api/v1/tickets/sync", "", 200)
	rec(RoleAdmin, "POST", "/api/v1/tickets/sync", "/api/v1/tickets/sync", `{}`, 503)
	f.srv.Sync = &tsync.Syncer{Primary: f.srv.Tickets.Primary, Mirror: memory.New("paperclip"), Links: f.store, Marks: f.store}
	rec(RoleAdmin, "POST", "/api/v1/tickets/sync", "/api/v1/tickets/sync", `{"initial":true,"dry_run":true}`, 200)
	rec(RoleMonitor, "GET", "/api/v1/tickets/sync", "/api/v1/tickets/sync", "", 200)
	f.srv.Sync = nil
	f.srv.Tickets = nil

	const prune = "/api/v1/gates/prune"
	rec(RoleAdmin, "POST", prune, prune, `{"days":7,"keep_per_workspace":5}`, 200)
	rec(RoleAdmin, "POST", prune, prune, `{"days":36501}`, 400)
	rec(RoleAdmin, "POST", prune, prune, `{"keep_per_workspace":-1}`, 400)
	rec(RoleAdmin, "POST", prune, prune, `{"days":99999999999999999}`, 400)

	dir := os.Getenv("DOCKWEAVE_OPENAPI_FIXTURES")
	if dir == "" {
		return
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pipeline-responses.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
