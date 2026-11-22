package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/memory"
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
