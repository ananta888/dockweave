package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// RF-038: the bundle embedded into the Go binary is complete. Every asset the
// served index.html references is delivered by the Go server itself with
// its own content (not the SPA fallback, which also answers 200).
func TestEmbeddedBundleServesEveryReferencedAsset(t *testing.T) {
	srv, err := New(Config{DaemonSocket: "/nonexistent", TermSocket: "/nonexistent", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.static(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec
	}
	index := get("/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "<app-root") {
		t.Fatalf("index: %d %.200s", index.Code, index.Body)
	}
	refs := regexp.MustCompile(`(?:src|href)="([^"#?:]+\.(?:js|css|ico))"`).FindAllStringSubmatch(index.Body.String(), -1)
	var js, css int
	for _, m := range refs {
		ref := m[1]
		rec := get("/" + strings.TrimPrefix(ref, "/"))
		want, err := fs.ReadFile(srv.assets, strings.TrimPrefix(ref, "/"))
		if err != nil {
			t.Fatalf("index.html references %s, which is not embedded (stale bundle?)", ref)
		}
		if rec.Code != http.StatusOK || rec.Body.String() != string(want) || strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s served as %d %q", ref, rec.Code, rec.Header().Get("Content-Type"))
		}
		switch {
		case strings.HasSuffix(ref, ".js"):
			js++
		case strings.HasSuffix(ref, ".css"):
			css++
		}
	}
	if js < 2 || css < 1 {
		t.Fatalf("unexpectedly few assets referenced (js=%d css=%d): %v", js, css, refs)
	}
}
