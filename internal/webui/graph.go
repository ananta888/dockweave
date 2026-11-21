package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/gateexec"
	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/pkg/sdk"
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
	s.graphMu.Lock()
	defer s.graphMu.Unlock()
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
	state := readGraphSync(path, graphRevision(body))
	writeJSON(w, http.StatusOK, state.response(body))
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := decoder.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode graph: %w", err))
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("graph must contain exactly one JSON object"))
		return
	}
	if err := validateGraphObject(body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	path, err := graphPath()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}

	// Optimistic concurrency (GD-014): reject a save based on a stale revision
	// so two browser tabs cannot silently overwrite each other. The lock makes
	// check, write and gate sync one atomic step within this gateway.
	s.graphMu.Lock()
	defer s.graphMu.Unlock()

	clientRev := graphRevision(body)
	currentRev := 0
	previous, err := os.ReadFile(path)
	switch {
	case err == nil:
		currentRev = graphRevision(previous)
		if clientRev != currentRev {
			writeJSONError(w, http.StatusConflict,
				fmt.Errorf("graph changed on disk (revision %d, yours %d); reload and retry", currentRev, clientRev))
			return
		}
	case os.IsNotExist(err):
		previous = nil
	default:
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("read graph: %w", err))
		return
	}
	nextRev := currentRev + 1
	if body, err = stampRevision(body, nextRev); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}

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
	// The previous document lets the sync clear workspaces that lost all gates.
	state := s.synchronizeGraph(r.Context(), path, body, previous, nextRev, previous == nil)
	writeJSON(w, http.StatusOK, state.response(body))
}

// PipelineSync reports the transfer of the derived pipeline edges.
type PipelineSync struct {
	Pushed   bool     `json:"pushed"`
	Edges    int      `json:"edges"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
	// Code is the daemon's structured error code for a failed push, e.g.
	// stale_revision (an older graph than enforced) or revision_conflict
	// (different content at the enforced revision), forbidden, unavailable.
	Code string `json:"code,omitempty"`
	// InvalidConnector names the gate connector whose contract stopped the
	// sync; the previously enforced edges stay in place (RF-001).
	InvalidConnector *InvalidConnector `json:"invalid_connector,omitempty"`
}

// InvalidConnector is the structured form of gatesync.ContractError.
type InvalidConnector struct {
	ConnectorID   string `json:"connector_id"`
	TargetAgentID string `json:"target_agent_id,omitempty"`
	Message       string `json:"message"`
}

// syncPipeline derives the gate -> agent edges and pushes them to the daemon
// with the new graph revision (reset when the graph file was recreated). A
// failure never loses the saved graph; it is logged and reported so the UI
// can show that enforcement is out of date.
func (s *Server) syncPipeline(ctx context.Context, body []byte, rev int, reset bool) PipelineSync {
	edges, warnings, err := gatesync.DerivePipeline(body, s.browseRoots)
	out := PipelineSync{Edges: len(edges), Warnings: warnings}
	if err != nil {
		out.Error = err.Error()
	} else if s.pushPipeline == nil {
		if len(edges) > 0 {
			out.Error = "no daemon configured: pipeline edges are not enforced"
		}
	} else {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := s.pushPipeline(pctx, rev, reset, edges); err != nil {
			out.Error = "pipeline transfer to the daemon failed: " + err.Error()
			var apiErr *sdk.APIError
			if errors.As(err, &apiErr) {
				out.Code = apiErr.Code
			} else {
				out.Code = "unavailable"
			}
		} else {
			out.Pushed = true
		}
	}
	if out.Error != "" && s.cfg.Logger != nil {
		s.cfg.Logger.Warn("pipeline_sync.error", "error", out.Error, "revision", rev, "edges", len(edges))
	} else if out.Pushed && s.cfg.Logger != nil {
		s.cfg.Logger.Info("pipeline synced", "revision", rev, "edges", len(edges))
	}
	return out
}

// validateExpression checks a gate expression for the editor without running
// anything: syntax and variable names, plus whether it needs a budget.
func (s *Server) validateExpression(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expression       string `json:"expression"`
		BudgetConfigured bool   `json:"budget_configured"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	out := map[string]any{"ok": true, "uses_budget": false}
	if strings.TrimSpace(req.Expression) == "" {
		// Empty means "the exit code decides" and is always valid.
		writeJSON(w, http.StatusOK, out)
		return
	}
	v, err := gateexec.Validate(req.Expression)
	if err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["uses_budget"] = v.UsesBudget
	if v.UsesBudget && !req.BudgetConfigured {
		out["warning"] = "the expression reads budget, but the contract has no budget; the gate will end with status error at run time (fail-closed)"
	}
	writeJSON(w, http.StatusOK, out)
}

// graphRevision reads the revision field of a graph document (default 0).
func graphRevision(body json.RawMessage) int {
	var probe struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Revision
}

// stampRevision sets the document's revision field. Only that field is
// replaced; every other value is kept byte-for-byte (json.RawMessage), so
// large integers or precise numbers survive the round trip.
func stampRevision(body json.RawMessage, rev int) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("graph must be an object: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("graph must be an object")
	}
	m["revision"] = json.RawMessage(strconv.Itoa(rev))
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	return out, nil
}

func validateGraphObject(body json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("graph must be an object: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("graph must be an object")
	}
	if raw, ok := fields["revision"]; ok {
		var rev int
		if string(raw) == "null" {
			return fmt.Errorf("revision must be a nonnegative integer")
		}
		if err := json.Unmarshal(raw, &rev); err != nil || rev < 0 {
			return fmt.Errorf("revision must be a nonnegative integer")
		}
	}
	return nil
}
