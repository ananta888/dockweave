package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/ananta888/dockweave/internal/gatesync"
)

type graphSyncState struct {
	Revision int               `json:"revision"`
	Gates    gatesync.Result   `json:"gates"`
	GatesAll []gatesync.Result `json:"gates_all"`
	Pipeline PipelineSync      `json:"pipeline_sync"`
	Bindings map[string]string `json:"pipeline_bindings"`
	Previous json.RawMessage   `json:"previous,omitempty"`
	// PendingReset records that this revision's pipeline push needs a daemon
	// revision reset (recreated graph file) that has not been confirmed yet.
	// Only this server-side record, never the caller, can request a reset.
	PendingReset bool `json:"pending_reset,omitempty"`
}

func (s graphSyncState) response(body json.RawMessage) map[string]any {
	return map[string]any{"saved": true, "body": body, "revision": s.Revision,
		"gates": s.Gates, "gates_all": s.GatesAll, "pipeline_sync": s.Pipeline, "pipeline_bindings": s.Bindings}
}

func readGraphSync(path string, rev int) graphSyncState {
	out := graphSyncState{Revision: rev, Pipeline: PipelineSync{Error: "pipeline synchronization has not been confirmed for this graph revision"}}
	raw, err := os.ReadFile(path + ".sync.json")
	if err != nil {
		return out
	}
	var stored graphSyncState
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Revision != rev {
		return out
	}
	return stored
}

func (s *Server) synchronizeGraph(ctx context.Context, path string, body, previous json.RawMessage, rev int, reset bool) graphSyncState {
	results, warnings := gatesync.SyncAll(body, previous, s.browseRoots)
	out := graphSyncState{Revision: rev, GatesAll: results, Gates: gatesync.Result{Warnings: warnings}, Previous: previous}
	for _, res := range results {
		if res.Error != "" {
			out.Gates.Error += res.Error + "; "
			continue
		}
		out.Gates.Written = out.Gates.Written || res.Written
		out.Gates.Gates += res.Gates
		out.Gates.Review = out.Gates.Review || res.Review
		out.Gates.HumanApproval = out.Gates.HumanApproval || res.HumanApproval
	}
	_, bindings, _, err := gatesync.DerivePipelineBindings(body, s.browseRoots)
	if err == nil {
		out.Bindings = bindings
	}
	// A partial gate-file sync must not publish a different enforcement set.
	if out.Gates.Error != "" {
		out.Pipeline.Error = "gate synchronization failed: " + out.Gates.Error
	} else {
		out.Pipeline = s.syncPipeline(ctx, body, rev, reset)
	}
	out.PendingReset = reset && !out.Pipeline.Pushed
	data, err := json.Marshal(out)
	if err == nil {
		err = os.WriteFile(path+".sync.json.tmp", data, 0o600)
	}
	if err == nil {
		err = os.Rename(path+".sync.json.tmp", path+".sync.json")
	}
	if err != nil {
		out.Pipeline.Error += " synchronization status could not be saved: " + err.Error()
	}
	return out
}

// retryGraphSync always re-reads the current document under the same save lock.
// A stale caller cannot restore old edges or request a revision reset.
func (s *Server) retryGraphSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision *int `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&req); err != nil || req.Revision == nil || *req.Revision < 0 {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("revision must be a nonnegative integer"))
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("expected a single JSON object"))
		return
	}
	s.graphMu.Lock()
	defer s.graphMu.Unlock()
	path, err := graphPath()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusConflict, fmt.Errorf("load current graph: %w", err))
		return
	}
	if err := validateGraphObject(body); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	rev := graphRevision(body)
	if *req.Revision != rev {
		writeJSONError(w, http.StatusConflict, fmt.Errorf("graph changed; reload before retrying synchronization"))
		return
	}
	old := readGraphSync(path, rev)
	out := s.synchronizeGraph(r.Context(), path, body, old.Previous, rev, old.PendingReset)
	writeJSON(w, http.StatusOK, out.response(body))
}
