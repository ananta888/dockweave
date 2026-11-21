package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The 2D graph (team layout, gates, contracts) used to live only in the
// browser's localStorage. It is stored here so the wrappers, the web UI and
// future adapters read the same document.
const graphDocumentID = "team"

type graphDocument struct {
	ID    string          `json:"id"`
	Body  json.RawMessage `json:"body"`
	Saved bool            `json:"saved"`
}

func (s *Server) getGraph(w http.ResponseWriter, r *http.Request) {
	raw, err := s.Store.GraphDocument(r.Context(), graphDocumentID)
	if err != nil {
		s.fail(w, err)
		return
	}
	doc := graphDocument{ID: graphDocumentID, Saved: strings.TrimSpace(raw) != ""}
	if doc.Saved {
		doc.Body = json.RawMessage(raw)
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) putGraph(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := decoder.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode graph: %w", err))
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("graph must contain exactly one JSON object"))
		return
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil || probe == nil {
		if err == nil {
			err = errors.New("null is not an object")
		}
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("graph must be a JSON object: %w", err))
		return
	}
	if rev, ok := probe["revision"]; ok {
		var n int
		if string(rev) == "null" {
			writeErr(w, http.StatusBadRequest, "invalid", errors.New("revision must be a nonnegative integer"))
			return
		}
		if err := json.Unmarshal(rev, &n); err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid", errors.New("revision must be a nonnegative integer"))
			return
		}
	}
	if err := s.Store.SaveGraphDocument(r.Context(), graphDocumentID, string(body)); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graphDocument{ID: graphDocumentID, Body: body, Saved: true})
}
