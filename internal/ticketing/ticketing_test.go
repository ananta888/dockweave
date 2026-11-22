package ticketing

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		in, def string
		want    Ref
		err     bool
	}{
		{"paperclip:PC-12", "", Ref{"paperclip", "PC-12"}, false},
		{"native:tk_1", "paperclip", Ref{"native", "tk_1"}, false},
		// Legacy bare Paperclip issue ids stay valid (ADR 0003 D-1).
		{"3f2a-uuid", "paperclip", Ref{"paperclip", "3f2a-uuid"}, false},
		{"3f2a-uuid", "", Ref{}, true},
		{"  ", "paperclip", Ref{}, true},
		{"http://x/y", "paperclip", Ref{"paperclip", "http://x/y"}, false},
	} {
		got, err := ParseRef(tc.in, tc.def)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ParseRef(%q,%q) = %+v, %v", tc.in, tc.def, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("error must wrap ErrInvalid: %v", err)
		}
	}
	if s := (Ref{"native", "tk_1"}).String(); s != "native:tk_1" {
		t.Fatal(s)
	}
}

type stub struct{ name string }

func (s stub) Name() string                { return s.name }
func (stub) Health(context.Context) Health { return Health{OK: true} }
func (stub) Capabilities() Capabilities    { return Capabilities{} }
func (stub) Tickets() Tickets              { return nil }
func (stub) Threads() Threads              { return nil }
func (stub) Inbox() Inbox                  { return nil }
func (stub) Waker() Waker                  { return nil }

func TestOpenSelectsPrimaryAndMirrorNeverFallsBack(t *testing.T) {
	Register("stub-a", func(Config) (Provider, error) { return stub{"stub-a"}, nil })
	Register("stub-b", func(Config) (Provider, error) { return stub{"stub-b"}, nil })
	sel, err := Open(Config{Primary: "STUB-A", Mirror: "stub-b"})
	if err != nil || sel.Primary.Name() != "stub-a" || sel.Mirror == nil || sel.Mirror.Name() != "stub-b" {
		t.Fatalf("open: %+v %v", sel, err)
	}
	if sel, err := Open(Config{Primary: "stub-a", Mirror: "none"}); err != nil || sel.Mirror != nil {
		t.Fatalf("no mirror: %+v %v", sel, err)
	}
	for _, cfg := range []Config{{}, {Primary: "unknown"}, {Primary: "stub-a", Mirror: "stub-a"}, {Primary: "stub-a", Mirror: "unknown"}} {
		if _, err := Open(cfg); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err=%v, want ErrInvalid (no silent fallback)", cfg, err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration must panic")
		}
	}()
	Register("stub-a", func(Config) (Provider, error) { return stub{}, nil })
}

// The port must not depend on any adapter (dependency inversion).
func TestPortImportsNoAdapter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		ast, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range ast.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(p, "github.com/ananta888/dockweave/internal/ticketing/") {
				t.Errorf("%s imports adapter %s", f, p)
			}
		}
	}
}
