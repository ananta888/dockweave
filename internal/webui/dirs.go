package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// dirEntry is one selectable directory.
type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// dirListing is the directory browser response.
type dirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"`
	Roots   []dirEntry `json:"roots"`
	Entries []dirEntry `json:"entries"`
}

// defaultBrowseRoots matches the daemon defaults plus the DockWeave state
// directories so default agent/memory folders are visible.
func defaultBrowseRoots() []string {
	return []string{"/home", "/srv", "/opt/projects", "/var/lib/dockweave"}
}

// listDirs serves the admin directory browser without proxying to the
// daemon. This keeps the web UI usable with an older daemon binary while
// still restricting listings to explicitly configured roots.
func (s *Server) listDirs(w http.ResponseWriter, r *http.Request) {
	resp := dirListing{Roots: s.roots()}
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		writeDirJSON(w, http.StatusOK, resp)
		return
	}
	canon, err := filepath.EvalSymlinks(raw)
	if err != nil {
		writeDirErr(w, http.StatusBadRequest, fmt.Errorf("resolve path: %w", err))
		return
	}
	if !s.underRoot(canon) {
		writeDirErr(w, http.StatusForbidden, errors.New("path is outside the browsable roots"))
		return
	}
	entries, err := os.ReadDir(canon)
	if err != nil {
		writeDirErr(w, http.StatusBadRequest, err)
		return
	}
	resp.Path = canon
	if parent := filepath.Dir(canon); parent != canon && s.underRoot(parent) {
		resp.Parent = parent
	}
	for _, e := range entries {
		if e.IsDir() {
			resp.Entries = append(resp.Entries, dirEntry{Name: e.Name(), Path: filepath.Join(canon, e.Name())})
		}
	}
	sort.Slice(resp.Entries, func(i, j int) bool { return resp.Entries[i].Name < resp.Entries[j].Name })
	writeDirJSON(w, http.StatusOK, resp)
}

func (s *Server) roots() []dirEntry {
	seen := map[string]bool{}
	var out []dirEntry
	for _, r := range s.browseRoots {
		canon, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		fi, err := os.Stat(canon)
		if err != nil || !fi.IsDir() || seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, dirEntry{Name: canon, Path: canon})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (s *Server) underRoot(p string) bool {
	for _, r := range s.browseRoots {
		canon, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(canon, p)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func writeDirJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeDirErr(w http.ResponseWriter, status int, err error) {
	writeDirJSON(w, status, map[string]string{"error": err.Error()})
}
