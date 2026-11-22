package authz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicy(t *testing.T) {
	p, err := Compile(Default())
	if err != nil {
		t.Fatal(err)
	}
	admin := p.For([]string{"dockweave-admin"})
	if !admin.Has(TerminalTakeover) || !admin.Has(SyncRun) || len(admin.Permissions) != len(All) {
		t.Fatalf("admin: %+v", admin)
	}
	reader := p.For([]string{"dockweave-monitor"})
	if !reader.Has(TicketRead) || reader.Has(TicketComment) {
		t.Fatalf("reader: %+v", reader)
	}
	if g := p.For([]string{"random-group"}); len(g.Permissions) != 0 {
		t.Fatalf("unknown group must grant nothing: %+v", g)
	}
}

func TestCompileRejectsTypos(t *testing.T) {
	for _, c := range []Config{
		{Roles: map[string][]string{"x": {"ticket.raed"}}},
		{Roles: map[string][]string{"x": {"ticket.read"}}, Groups: map[string][]string{"g": {"y"}}},
		{Roles: map[string][]string{"": {"ticket.read"}}},
	} {
		if _, err := Compile(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestStoreReloadsAndFallsBackReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-roles.json")
	var reloads []error
	s := &Store{Path: path, CheckEvery: time.Nanosecond, OnReload: func(err error) { reloads = append(reloads, err) }}
	// Missing file: defaults.
	if !s.Policy().For([]string{"dockweave-admin"}).Has(TicketDelete) || s.Error() != nil {
		t.Fatal("missing file must use defaults")
	}
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		// Different size or mtime makes the change visible.
		_ = os.Chtimes(path, time.Now(), time.Now().Add(time.Duration(len(reloads))*time.Second))
	}
	write(`{"groups":{"dw-author":["author"]},"roles":{"author":["ticket.read","ticket.create","ticket.comment"]}}`)
	g := s.Policy().For([]string{"dw-author"})
	if !g.Has(TicketCreate) || g.Has(TicketAssign) || s.Error() != nil {
		t.Fatalf("custom: %+v %v", g, s.Error())
	}
	// A broken file degrades humans to read-only and says why; it never
	// keeps granting the old rights.
	write(`{"groups":{"dw-author":["author"]},"roles":{"author":["ticket.everything"]}}`)
	g = s.Policy().For([]string{"dw-author"})
	if g.Has(TicketCreate) || !g.Has(TicketRead) || s.Error() == nil || !strings.Contains(s.Error().Error(), "read-only") {
		t.Fatalf("invalid: %+v %v", g, s.Error())
	}
	write(`{"unknown_key":1}`)
	if s.Error() == nil {
		t.Fatal("unknown key accepted")
	}
	// Fixing the file restores the rights without a restart.
	write(`{"groups":{"dw-author":["author"]},"roles":{"author":["ticket.read","ticket.assign"]}}`)
	if g := s.Policy().For([]string{"dw-author"}); !g.Has(TicketAssign) || s.Error() != nil {
		t.Fatalf("fixed: %+v %v", g, s.Error())
	}
	if len(reloads) < 4 {
		t.Fatalf("reload notifications: %d", len(reloads))
	}
}
