package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ananta888/dockweave/internal/gatesync"
)

// graphPath is where the browser graph document is persisted. It used to live
// only in localStorage; now the web gateway is the shared source of truth so
// the wrapper and future adapters can read the same gates.
func writeJSONError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func graphPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "dockweave", "graph.json"), nil
}

func (s *Server) getGraph(w http.ResponseWriter, r *http.Request) {
	path, err := graphPath()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, map[string]any{"saved": false, "body": nil, "revision": 0})
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	var body json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("graph file is not JSON: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "body": body, "revision": graphRevision(body)})
}

// getGateStatus reports whether the workspace gates.json still matches the
// file DockWeave wrote (drift detection).
func (s *Server) getGateStatus(w http.ResponseWriter, r *http.Request) {
	path, err := graphPath()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, gatesync.StatusResult{Enabled: false})
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, gatesync.Status(raw, s.browseRoots))
}

func (s *Server) putGraph(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode graph: %w", err))
		return
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("graph must be an object: %w", err))
		return
	}
	path, err := graphPath()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}

	// Optimistic concurrency (GD-014): reject a save based on a stale revision
	// so two browser tabs cannot silently overwrite each other.
	clientRev := graphRevision(body)
	currentRev := 0
	if existing, err := os.ReadFile(path); err == nil {
		currentRev = graphRevision(existing)
	}
	if currentRev > 0 && clientRev != currentRev {
		writeJSONError(w, http.StatusConflict,
			fmt.Errorf("graph changed on disk (revision %d); reload and retry", currentRev))
		return
	}
	nextRev := currentRev + 1
	body = stampRevision(body, nextRev)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	// Best-effort mirror of the graph gate connectors into the Paperclip
	// workspace(s). A sync failure must never lose the graph document the user
	// just saved, so it is reported in the response instead of failing PUT.
	results := gatesync.SyncAll(body, s.browseRoots)
	aggregate := gatesync.Result{}
	for _, res := range results {
		if res.Error != "" {
			if s.cfg.Logger != nil {
				s.cfg.Logger.Warn("paperclip gate sync failed", "error", res.Error, "workspace", res.Workspace)
			}
			if aggregate.Error == "" {
				aggregate.Error = res.Error
			}
			continue
		}
		if res.Skipped {
			continue
		}
		if res.Written {
			aggregate.Written = true
			aggregate.Gates += res.Gates
			aggregate.Review = aggregate.Review || res.Review
			aggregate.HumanApproval = aggregate.HumanApproval || res.HumanApproval
			aggregate.Warnings = append(aggregate.Warnings, res.Warnings...)
			if s.cfg.Logger != nil {
				s.cfg.Logger.Info("paperclip gates synced", "path", res.Path, "gates", res.Gates)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved": true, "body": body, "revision": nextRev, "gates": aggregate, "gates_all": results,
	})
}

// graphRevision reads the revision field of a graph document (default 0).
func graphRevision(body json.RawMessage) int {
	var probe struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Revision
}

// stampRevision rewrites the document with the given revision field set.
func stampRevision(body json.RawMessage, rev int) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["revision"] = rev
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
