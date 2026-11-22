package paperclip

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/contracttest"
	"github.com/ananta888/dockweave/internal/ticketing/paperclip/paperclipfake"
)

type dir map[string]string

func (d dir) Resolve(_ context.Context, nameOrID string) (string, bool) {
	id, ok := d[nameOrID]
	return id, ok
}

func agentsDir() (dir, []string) {
	d := dir{}
	var names []string
	for c := 'a'; c <= 'h'; c++ {
		id := "ag_" + string(c)
		d[id] = id
		names = append(names, id)
	}
	return d, names
}

func newProvider(t *testing.T) (*Provider, *paperclipfake.Server) {
	d, names := agentsDir()
	f := paperclipfake.New(t, names...)
	return New(Options{BaseURL: f.Srv.URL, Agents: d}), f
}

func TestPaperclipProviderContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) ticketing.Provider { p, _ := newProvider(t); return p })
}

func TestPaperclipMapsAgentsAndAuthorship(t *testing.T) {
	ctx := context.Background()
	p, f := newProvider(t)
	human := ticketing.Actor{Kind: ticketing.ActorHuman, ID: "iss|alice", Name: "Alice"}
	tk, err := p.Create(ctx, ticketing.NewTicket{Title: "x", Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_b"}}, human)
	if err != nil {
		t.Fatal(err)
	}
	if f.Issues[tk.Ref.ID]["assigneeAgentId"] != paperclipfake.UUID(1001) || tk.Assignee.ID != "ag_b" || tk.Identifier != "PC-1" {
		t.Fatalf("assignee mapping: %+v / %v", tk, f.Issues[tk.Ref.ID]["assigneeAgentId"])
	}
	// The Paperclip author is its API identity (no Authorship capability).
	if tk.CreatedBy == nil || tk.CreatedBy.ID != UserPrefix+"local-board" || p.Capabilities().Authorship {
		t.Fatalf("created_by: %+v", tk.CreatedBy)
	}
	// A comment keeps the DockWeave author through the body marker, and the
	// Paperclip UI sees a readable attribution line.
	c, err := p.AddComment(ctx, tk.Ref.ID, "looks -- good", human)
	if err != nil {
		t.Fatal(err)
	}
	stored := f.Comments[tk.Ref.ID][0]["body"].(string)
	if !strings.Contains(stored, "— Alice (via DockWeave)") || !strings.Contains(stored, markerPrefix) {
		t.Fatalf("stored body: %q", stored)
	}
	if c.Body != "looks -- good" || c.Author != human {
		t.Fatalf("decoded comment: %+v", c)
	}
	// A Paperclip agent without a DockWeave binding stays recognisable.
	f.Issues[tk.Ref.ID]["assigneeAgentId"] = paperclipfake.UUID(999)
	got, _ := p.Get(ctx, tk.Ref.ID)
	if got.Assignee.ID != ForeignAgentPrefix+paperclipfake.UUID(999) || got.Assignee.Name != "Claude CEO" {
		t.Fatalf("foreign agent: %+v", got.Assignee)
	}
	// Assigning a DockWeave agent without a Paperclip process agent fails clearly.
	if _, err := p.Create(ctx, ticketing.NewTicket{Title: "y", Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_unknown"}}, human); !errors.Is(err, ticketing.ErrInvalid) {
		t.Fatalf("unmapped agent: %v", err)
	}
	// Humans need a Paperclip user id.
	if _, err := p.Create(ctx, ticketing.NewTicket{Title: "z", Assignee: &human}, human); !errors.Is(err, ticketing.ErrInvalid) {
		t.Fatalf("unmapped human: %v", err)
	}
	if err := p.Wake(ctx, "ag_c"); err != nil || len(f.Wakes) != 1 || f.Wakes[0] != paperclipfake.UUID(1002) {
		t.Fatalf("wake: %v %v", err, f.Wakes)
	}
}

func TestPaperclipStatusRaceUsesStatusVersion(t *testing.T) {
	ctx := context.Background()
	p, f := newProvider(t)
	tk, _ := p.Create(ctx, ticketing.NewTicket{Title: "x"}, ticketing.Actor{Kind: ticketing.ActorSystem})
	// Someone else changes the status between our read and our write: our
	// stale ExpectedVersion is refused before writing.
	f.Issues[tk.Ref.ID]["status"] = "blocked"
	f.Issues[tk.Ref.ID]["statusVersion"] = 2
	done := ticketing.StatusDone
	if _, err := p.Update(ctx, tk.Ref.ID, ticketing.Update{Status: &done, ExpectedVersion: tk.Version}, ticketing.Actor{Kind: ticketing.ActorSystem}); !errors.Is(err, ticketing.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
}

func TestPaperclipUnreachableFailsClosed(t *testing.T) {
	p := New(Options{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{}})
	if h := p.Health(context.Background()); h.OK || h.Detail == "" {
		t.Fatalf("health: %+v", h)
	}
	if _, err := p.List(context.Background(), ticketing.Filter{}); !errors.Is(err, ticketing.ErrUnavailable) {
		t.Fatalf("list: %v", err)
	}
}

func TestMarkerRoundTripAndForeignBodies(t *testing.T) {
	a := ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_a", Name: "Alpha"}
	enc := encodeMarker("line1\n\n-- not a marker --", marker{Actor: a, Origin: "native:tc_1"})
	body, m, ok := decodeMarker(enc)
	if !ok || body != "line1\n\n-- not a marker --" || m.Actor != a || m.Origin != "native:tc_1" {
		t.Fatalf("roundtrip: %q %+v %v", body, m, ok)
	}
	for _, s := range []string{"plain", "x <!-- dockweave:{broken -->", "<!-- dockweave:{} --> trailing"} {
		if got, _, ok := decodeMarker(s); ok || got != s {
			t.Fatalf("foreign body %q decoded as %q", s, got)
		}
	}
}

// Read-only smoke test against a real Paperclip (never writes):
// DOCKWEAVE_PAPERCLIP_SMOKE_URL=http://127.0.0.1:3100 go test ./internal/ticketing/paperclip -run Smoke
func TestPaperclipSmokeReadOnly(t *testing.T) {
	base := os.Getenv("DOCKWEAVE_PAPERCLIP_SMOKE_URL")
	if base == "" {
		t.Skip("DOCKWEAVE_PAPERCLIP_SMOKE_URL not set")
	}
	ctx := context.Background()
	p := New(Options{BaseURL: base, Agents: dir{}})
	if h := p.Health(ctx); !h.OK {
		t.Fatalf("health: %+v", h)
	}
	list, err := p.List(ctx, ticketing.Filter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range list {
		got, err := p.Get(ctx, tk.Ref.ID)
		if err != nil || got.Version == "" || !got.Status.Valid() || got.CreatedAt.IsZero() {
			t.Fatalf("get %s: %+v %v", tk.Ref.ID, got, err)
		}
		if _, err := p.Comments(ctx, tk.Ref.ID); err != nil {
			t.Fatalf("comments %s: %v", tk.Ref.ID, err)
		}
	}
	t.Logf("read %d tickets", len(list))
}

func TestVersionCheck(t *testing.T) {
	for v, want := range map[string]bool{"2026.1106.0": true, "2026.1106.3": true, "2026.1201.0": true, "2027.101.0": true,
		"2026.1105.9": false, "2025.1231.0": false, "": false, "dev": false} {
		if got := versionAtLeast(v, MinVersion); got != want {
			t.Errorf("versionAtLeast(%q) = %v", v, got)
		}
	}
}
