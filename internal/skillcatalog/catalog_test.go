package skillcatalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledStandardSkill(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()
	if len(snap.Bundled) != 1 || snap.Bundled[0].Name != "keycloak-oidc-human-guide" {
		t.Fatalf("bundled = %+v", snap.Bundled)
	}
	if !strings.Contains(snap.Bundled[0].Description, "Keycloak") {
		t.Errorf("description = %q", snap.Bundled[0].Description)
	}
	if _, err := os.Stat(filepath.Join(snap.Bundled[0].Path, "references", "foundations.md")); err != nil {
		t.Errorf("reference not materialized: %v", err)
	}
	if len(snap.Stores) < 3 {
		t.Errorf("stores = %d", len(snap.Stores))
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallFromStore(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"skills-main/skills/demo/SKILL.md":        "---\nname: demo\ndescription: \"A demo skill\"\n---\n# Demo\n",
		"skills-main/skills/demo/references/a.md": "reference",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/anthropics/skills/tar.gz/main") {
			t.Errorf("unexpected URL %s", r.URL.Path)
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.archiveBase = srv.URL
	e, err := c.Install(context.Background(), "anthropics/skills", "skills/demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "demo" || e.Description != "A demo skill" {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(e.Path, "references", "a.md")); err != nil {
		t.Errorf("reference missing: %v", err)
	}
	if _, ok := c.Path("demo"); !ok {
		t.Error("installed skill not resolvable")
	}
	if err := c.Delete("demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Path("demo"); ok {
		t.Error("skill still present after delete")
	}
}

func TestInstallRejectsUnsafeInput(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ repo, path string }{
		{"evil/repo", "skills/x"},
		{"anthropics/skills", "../etc"},
		{"anthropics/skills", "skills/../../x"},
		{"anthropics/skills", ""},
	} {
		if _, err := c.Install(context.Background(), tc.repo, tc.path, "main"); err == nil {
			t.Errorf("repo=%q path=%q: expected error", tc.repo, tc.path)
		}
	}
}

func TestParseFrontmatter(t *testing.T) {
	name, desc := parseFrontmatter("---\nname: x\ndescription: >\n  line one\n  line two\n---\nbody")
	if name != "x" || desc != "line one line two" {
		t.Fatalf("name=%q desc=%q", name, desc)
	}
}
