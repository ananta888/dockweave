package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ananta888/dockweave/internal/credentialbroker"
)

// Credential management endpoints. They proxy the broker's admin API over the
// daemon's admin socket; secret values are write-only and never returned.
func (s *Server) credentialUnavailable(w http.ResponseWriter) bool {
	if s.Broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "broker_unavailable",
			errors.New("credential broker admin socket is not configured"))
		return true
	}
	return false
}

func (s *Server) credentialStatus(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	st, err := s.Broker.Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) credentialSecrets(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	list, err := s.Broker.ListSecrets(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) credentialPutSecret(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Value    string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(req.Provider) == "" || req.Value == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("name, provider and value are required"))
		return
	}
	if err := s.Broker.PutSecret(r.Context(), name, req.Provider, req.Value); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stored", "name": name})
}

func (s *Server) credentialDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	if err := s.Broker.DeleteSecret(r.Context(), r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) credentialGrants(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	list, err := s.Broker.ListGrants(r.Context(), r.URL.Query().Get("agent"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) credentialAddGrant(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	var g credentialbroker.Grant
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	out, err := s.Broker.AddGrant(r.Context(), g)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) credentialDeleteGrant(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	if err := s.Broker.DeleteGrant(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) credentialSessions(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	list, err := s.Broker.ListSessions(r.Context(), r.URL.Query().Get("agent"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) credentialMintSession(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	var req struct {
		Agent      string `json:"agent"`
		Credential string `json:"credential"`
		GrantID    string `json:"grant_id"`
		TTL        string `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if req.Agent == "" || req.Credential == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("agent and credential are required"))
		return
	}
	if req.TTL == "" {
		req.TTL = "1h"
	}
	mint, err := s.Broker.MintSession(r.Context(), req.Agent, req.GrantID, req.Credential, req.TTL)
	if err != nil {
		s.fail(w, err)
		return
	}
	// The plaintext token is returned exactly once; it is not stored by the broker.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":   mint.Token,
		"session": mint.Session,
	})
}

func (s *Server) credentialRevokeSession(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	if err := s.Broker.RevokeSession(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) credentialRequests(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	list, err := s.Broker.Requests(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) credentialUsage(w http.ResponseWriter, r *http.Request) {
	if s.credentialUnavailable(w) {
		return
	}
	grant := r.URL.Query().Get("grant")
	if grant == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("grant is required"))
		return
	}
	usage, err := s.Broker.Usage(r.Context(), grant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
