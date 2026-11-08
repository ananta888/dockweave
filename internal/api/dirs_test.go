package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirsRootsAndEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{Root: root, AllowedRoots: []string{root}}
	ctx := ContextWithPeer(context.Background(), "test", 1000, RoleAdmin)

	// Empty path -> roots.
	rec := httptest.NewRecorder()
	s.listDirs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs", nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("roots status = %d", rec.Code)
	}
	var roots DirListing
	_ = json.Unmarshal(rec.Body.Bytes(), &roots)
	if len(roots.Roots) != 1 || roots.Roots[0].Path != root {
		t.Fatalf("roots = %+v", roots.Roots)
	}

	// Path -> entries.
	rec = httptest.NewRecorder()
	s.listDirs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs?path="+filepath.Join(root, "projects"), nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var list DirListing
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Entries) != 1 || list.Entries[0].Name != "a" {
		t.Fatalf("entries = %+v", list.Entries)
	}
	if list.Parent != root {
		t.Fatalf("parent = %q, want %q", list.Parent, root)
	}
}

func TestListDirsRejectsOutsideAndNonAdmin(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	s := &Server{Root: root, AllowedRoots: []string{root}}
	admin := ContextWithPeer(context.Background(), "admin", 1000, RoleAdmin)
	rec := httptest.NewRecorder()
	s.listDirs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs?path="+outside, nil).WithContext(admin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside status = %d, want 403", rec.Code)
	}
	monitor := ContextWithPeer(context.Background(), "monitor", 1001, RoleMonitor)
	rec = httptest.NewRecorder()
	s.listDirs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs", nil).WithContext(monitor))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("monitor status = %d, want 403", rec.Code)
	}
}
