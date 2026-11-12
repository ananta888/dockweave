// Package api implements the versioned DockWeave management API (v1),
// served over a Unix domain socket with SO_PEERCRED authorization.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/brokerclient"
	"github.com/ananta888/dockweave/internal/configio"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/monitor"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/workspaces"
)

// APIError is the structured error format of the API.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error APIError `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: APIError{Code: code, Message: err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, persistence.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, workspaces.ErrConflict), errors.Is(err, agents.ErrMemoryInUse):
		return http.StatusConflict, "conflict"
	case errors.Is(err, mounts.ErrBusy), errors.Is(err, memory.ErrBusy):
		return http.StatusConflict, "busy"
	case errors.Is(err, agents.ErrMemoryUnavailable):
		return http.StatusServiceUnavailable, "memory_unavailable"
	default:
		if isValidation(err) {
			return http.StatusBadRequest, "invalid"
		}
		return http.StatusInternalServerError, "internal"
	}
}

func isValidation(err error) bool {
	m := err.Error()
	return strings.Contains(m, "invalid") || strings.Contains(m, "must") || strings.Contains(m, "not allowed")
}

// Server is the v1 API handler.
type Server struct {
	Agents     *agents.Service
	Workspaces *workspaces.Service
	Monitor    *monitor.Monitor
	Store      *persistence.Store
	ConfigIO   *configio.Service
	// Broker is optional. When set, read-only broker metadata is exposed
	// through admin/monitor GET endpoints. No secret values cross this API.
	Broker *brokerclient.Client

	// AllowedRoots, Root and KnowledgeDir back the admin directory browser.
	// Only directories below these roots are ever listed.
	AllowedRoots []string
	Root         string
	KnowledgeDir string

	mux *http.ServeMux
}

// NewServer builds the HTTP handler tree.
func NewServer(a *agents.Service, w *workspaces.Service, m *monitor.Monitor, st *persistence.Store) *Server {
	s := &Server{Agents: a, Workspaces: w, Monitor: m, Store: st, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.health)
	s.mux.HandleFunc("GET /api/v1/agents", s.listAgents)
	s.mux.HandleFunc("POST /api/v1/agents", s.createAgent)
	s.mux.HandleFunc("GET /api/v1/agents/{id}", s.getAgent)
	s.mux.HandleFunc("DELETE /api/v1/agents/{id}", s.removeAgent)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/start", s.agentAction(s.Agents.Start))
	s.mux.HandleFunc("POST /api/v1/agents/{id}/stop", s.stopAgent)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/restart", s.agentAction(s.Agents.Restart))
	s.mux.HandleFunc("POST /api/v1/agents/{id}/memory/snapshot", s.snapshotMemory)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/memory/snapshots", s.listMemorySnapshots)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/memory/restore", s.restoreMemory)
	s.mux.HandleFunc("DELETE /api/v1/agents/{id}/memory/snapshots/{name}", s.deleteMemorySnapshot)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/memory/attach", s.attachMemory)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/memory/detach", s.detachMemory)
	s.mux.HandleFunc("GET /api/v1/memories", s.listMemories)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/session", s.agentSession)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/paths", s.agentPaths)
	s.mux.HandleFunc("PUT /api/v1/agents/{id}/agent-dir", s.setAgentDir)
	s.mux.HandleFunc("PUT /api/v1/agents/{id}/notes-dir", s.setNotesDir)
	s.mux.HandleFunc("PUT /api/v1/agents/{id}/env-file", s.setEnvFile)
	s.mux.HandleFunc("PUT /api/v1/agents/{id}/network-profile", s.setNetworkProfile)
	s.mux.HandleFunc("PUT /api/v1/agents/{id}/organization", s.setOrganization)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/mcp", s.listMCP)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/mcp", s.addMCP)
	s.mux.HandleFunc("PATCH /api/v1/agents/{id}/mcp/{mcp}", s.updateMCP)
	s.mux.HandleFunc("DELETE /api/v1/agents/{id}/mcp/{mcp}", s.deleteMCP)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/skills", s.listSkills)
	s.mux.HandleFunc("POST /api/v1/agents/{id}/skills", s.addSkill)
	s.mux.HandleFunc("PATCH /api/v1/agents/{id}/skills/{skill}", s.updateSkill)
	s.mux.HandleFunc("DELETE /api/v1/agents/{id}/skills/{skill}", s.deleteSkill)
	s.mux.HandleFunc("GET /api/v1/agents/{id}/egress", s.egressStatus)
	s.mux.HandleFunc("PATCH /api/v1/agents/{id}", s.updateAgent)
	s.mux.HandleFunc("POST /api/v1/memories", s.registerMemory)
	s.mux.HandleFunc("GET /api/v1/config/export", s.exportConfig)
	s.mux.HandleFunc("POST /api/v1/config/import", s.importConfig)
	s.mux.HandleFunc("GET /api/v1/workspaces", s.listWorkspaces)
	s.mux.HandleFunc("POST /api/v1/workspaces", s.registerWorkspace)
	s.mux.HandleFunc("DELETE /api/v1/workspaces/{id}", s.unregisterWorkspace)
	s.mux.HandleFunc("POST /api/v1/grants", s.createGrant)
	s.mux.HandleFunc("GET /api/v1/grants", s.listGrants)
	s.mux.HandleFunc("DELETE /api/v1/grants/{id}", s.revokeGrant)
	s.mux.HandleFunc("GET /api/v1/mounts", s.listMounts)
	s.mux.HandleFunc("GET /api/v1/activity", s.activity)
	s.mux.HandleFunc("GET /api/v1/conflicts", s.conflicts)
	s.mux.HandleFunc("GET /api/v1/events", s.events)
	s.mux.HandleFunc("GET /api/v1/events/stream", s.eventStream)
	s.mux.HandleFunc("GET /api/v1/monitor/providers", s.providers)
	s.mux.HandleFunc("POST /api/v1/monitor/reports", s.submitReport)
	s.mux.HandleFunc("GET /api/v1/broker/status", s.brokerStatus)
	s.mux.HandleFunc("GET /api/v1/broker/grants", s.brokerGrants)
	s.mux.HandleFunc("GET /api/v1/broker/sessions", s.brokerSessions)
	s.mux.HandleFunc("GET /api/v1/broker/requests", s.brokerRequests)
	s.mux.HandleFunc("GET /api/v1/broker/usage", s.brokerUsage)
	s.mux.HandleFunc("GET /api/v1/fs/dirs", s.listDirs)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// actor extracts the authenticated peer name from the request context.
func actor(r *http.Request) string {
	if v := r.Context().Value(peerKey{}); v != nil {
		return v.(string)
	}
	return "api"
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": "v1", "time": time.Now().UTC(),
	})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agents.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createAgentReq struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Image          string   `json:"image"`
	Command        []string `json:"command"`
	EnvFile        string   `json:"env_file"`
	AgentRO        *bool    `json:"agent_ro"`
	UID            int      `json:"uid"`
	GID            int      `json:"gid"`
	CPULimit       float64  `json:"cpu_limit"`
	MemoryMB       int64    `json:"memory_mb"`
	NetworkMode    string   `json:"network_mode"`
	NetworkProfile string   `json:"network_profile"`
	DisplayName    string   `json:"display_name"`
	OrgUnit        string   `json:"org_unit"`
	Role           string   `json:"role"`
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode body: %w", err))
		return
	}
	ro := true
	if req.AgentRO != nil {
		ro = *req.AgentRO
	}
	a := &model.Agent{
		Name: req.Name, Type: req.Type, Image: req.Image, Command: req.Command,
		EnvFile: req.EnvFile, AgentRO: ro, UID: req.UID, GID: req.GID,
		CPULimit: req.CPULimit, MemoryMB: req.MemoryMB, NetworkMode: req.NetworkMode,
		NetworkProfile: model.NetworkProfile(req.NetworkProfile),
		DisplayName:    req.DisplayName, OrgUnit: req.OrgUnit, Role: req.Role,
	}
	created, err := s.Agents.Create(r.Context(), a, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.Agents.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) removeAgent(w http.ResponseWriter, r *http.Request) {
	prune := r.URL.Query().Get("prune") == "true"
	force := r.URL.Query().Get("force") == "true"
	if err := s.Agents.Remove(r.Context(), r.PathValue("id"), actor(r), prune, force); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentAction(fn func(ctx context.Context, id, actor string) (*model.Agent, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := fn(r.Context(), r.PathValue("id"), actor(r))
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func (s *Server) stopAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.Agents.Stop(r.Context(), r.PathValue("id"), actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	list, err := s.Workspaces.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type registerWorkspaceReq struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) registerWorkspace(w http.ResponseWriter, r *http.Request) {
	var req registerWorkspaceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode body: %w", err))
		return
	}
	ws, err := s.Workspaces.Register(r.Context(), req.Name, req.Path, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (s *Server) unregisterWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.Workspaces.Unregister(r.Context(), r.PathValue("id"), actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type grantReq struct {
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Mode      string `json:"mode"`
	Exclusive bool   `json:"exclusive"`
	TTL       string `json:"ttl"` // e.g. "2h", empty = unlimited
}

func (s *Server) createGrant(w http.ResponseWriter, r *http.Request) {
	var req grantReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode body: %w", err))
		return
	}
	var ttl time.Duration
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("ttl: %w", err))
			return
		}
		ttl = d
	}
	g, err := s.Workspaces.Grant(r.Context(), req.Workspace, req.Agent, model.AccessMode(req.Mode), req.Exclusive, ttl, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.Workspaces.ListGrants(r.Context(), q.Get("workspace"), q.Get("agent"), q.Get("all") == "true")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if err := s.Workspaces.Revoke(r.Context(), r.PathValue("id"), actor(r), force); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listMounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Workspaces.ListMounts(r.Context(), r.URL.Query().Get("agent"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Agents.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Monitor.Collect(r.Context(), agents))
}

func (s *Server) conflicts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Monitor.Conflicts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	list, err := s.Store.ListEvents(r.Context(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// eventStream streams audit events as Server-Sent Events, polling the store.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	seen := map[string]bool{}
	// Skip history: mark current events seen.
	if list, err := s.Store.ListEvents(r.Context(), 200); err == nil {
		for _, e := range list {
			seen[e.ID] = true
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			list, err := s.Store.ListEvents(r.Context(), 100)
			if err != nil {
				continue
			}
			for i := len(list) - 1; i >= 0; i-- { // oldest first
				e := list[i]
				if seen[e.ID] {
					continue
				}
				seen[e.ID] = true
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
			}
			fl.Flush()
		}
	}
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Monitor.Providers(r.Context()))
}

// ---- memory snapshots ----

func (s *Server) snapshotMemory(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Agents.SnapshotMemory(r.Context(), r.PathValue("id"), actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) listMemorySnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agents.ListMemorySnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) restoreMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Snapshot string `json:"snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Snapshot == "" {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain snapshot name"))
		return
	}
	if err := s.Agents.RestoreMemorySnapshot(r.Context(), r.PathValue("id"), req.Snapshot, actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (s *Server) deleteMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	if err := s.Agents.DeleteMemorySnapshot(r.Context(), r.PathValue("id"), r.PathValue("name"), actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- memory identities (review-009) ----

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string   `json:"type"`
		Image   string   `json:"image"`
		Command []string `json:"command"`
		UID     *int     `json:"uid"`
		GID     *int     `json:"gid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	uid, gid := -1, -1
	if req.UID != nil {
		uid = *req.UID
	}
	if req.GID != nil {
		gid = *req.GID
	}
	a, err := s.Agents.UpdateAgentConfig(r.Context(), r.PathValue("id"),
		req.Type, req.Image, req.Command, uid, gid, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) setAgentDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain path"))
		return
	}
	a, err := s.Agents.SetAgentDir(r.Context(), r.PathValue("id"), req.Path, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) setNotesDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain path"))
		return
	}
	a, err := s.Agents.SetNotesDir(r.Context(), r.PathValue("id"), req.Path, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) setEnvFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain path"))
		return
	}
	a, err := s.Agents.SetEnvFile(r.Context(), r.PathValue("id"), req.Path, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) setOrganization(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName string `json:"display_name"`
		OrgUnit     string `json:"org_unit"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("invalid body: %w", err))
		return
	}
	a, err := s.Agents.SetOrganization(r.Context(), r.PathValue("id"), req.DisplayName, req.OrgUnit, req.Role, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) setNetworkProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain profile"))
		return
	}
	a, err := s.Agents.SetNetworkProfile(r.Context(), r.PathValue("id"), req.Profile, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) egressStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.Agents.EgressStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) registerMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain path"))
		return
	}
	mem, err := s.Agents.RegisterMemory(r.Context(), req.Path, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, mem)
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	b, err := s.ConfigIO.Export(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	var b configio.Bundle
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	res, err := s.ConfigIO.Import(r.Context(), &b)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agents.ListMemories(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) attachMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MemoryID == "" {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("body must contain memory_id"))
		return
	}
	mem, err := s.Agents.AttachMemory(r.Context(), r.PathValue("id"), req.MemoryID, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mem)
}

func (s *Server) detachMemory(w http.ResponseWriter, r *http.Request) {
	mem, err := s.Agents.DetachMemory(r.Context(), r.PathValue("id"), actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mem)
}

func (s *Server) agentPaths(w http.ResponseWriter, r *http.Request) {
	p, err := s.Agents.AgentPaths(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) agentSession(w http.ResponseWriter, r *http.Request) {
	info, err := s.Agents.SessionInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// monitorReport is the ONLY write surface of the restricted monitoring API:
// the AI monitor agent may submit analysis reports. Payload is strictly
// validated; no grants, mounts or agent state can be influenced.
type monitorReport struct {
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Findings []struct {
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"findings"`
	GeneratedAt string `json:"generated_at"`
}

func (s *Server) submitReport(w http.ResponseWriter, r *http.Request) {
	var rep monitorReport
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("decode body: %w", err))
		return
	}
	if len(rep.Title) > 200 || len(rep.Summary) > 4000 || len(rep.Findings) > 200 {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("report exceeds size limits"))
		return
	}
	// Serialize findings into a bounded message.
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s", rep.Title, rep.Summary)
	for i, f := range rep.Findings {
		if i >= 20 {
			fmt.Fprintf(&b, " … (%d more)", len(rep.Findings)-i)
			break
		}
		msg := f.Message
		if len(msg) > 300 {
			msg = msg[:300]
		}
		fmt.Fprintf(&b, "\n[%s/%s] %s", f.Kind, f.Severity, msg)
	}
	ev := &model.Event{
		ID: model.NewID("ev"), Type: model.EventType("monitor.report"),
		Actor: actor(r), Message: b.String(), Timestamp: time.Now(),
	}
	if err := s.Store.AppendEvent(r.Context(), ev); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"event_id": ev.ID})
}

// fail maps service errors to structured API errors.
func (s *Server) fail(w http.ResponseWriter, err error) {
	st, code := classify(err)
	writeErr(w, st, code, err)
}

// ---- broker metadata (read-only, no secrets) ----

func (s *Server) brokerUnavailable(w http.ResponseWriter) bool {
	if s.Broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "broker_unavailable",
			errors.New("credential broker admin socket is not configured"))
		return true
	}
	return false
}

func (s *Server) brokerStatus(w http.ResponseWriter, r *http.Request) {
	if s.brokerUnavailable(w) {
		return
	}
	st, err := s.Broker.Status(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "broker_unreachable", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) brokerGrants(w http.ResponseWriter, r *http.Request) {
	if s.brokerUnavailable(w) {
		return
	}
	list, err := s.Broker.ListGrants(r.Context(), r.URL.Query().Get("agent"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "broker_unreachable", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) brokerSessions(w http.ResponseWriter, r *http.Request) {
	if s.brokerUnavailable(w) {
		return
	}
	list, err := s.Broker.ListSessions(r.Context(), r.URL.Query().Get("agent"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "broker_unreachable", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) brokerRequests(w http.ResponseWriter, r *http.Request) {
	if s.brokerUnavailable(w) {
		return
	}
	list, err := s.Broker.Requests(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "broker_unreachable", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) brokerUsage(w http.ResponseWriter, r *http.Request) {
	if s.brokerUnavailable(w) {
		return
	}
	id := r.URL.Query().Get("grant")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("grant query parameter is required"))
		return
	}
	u, err := s.Broker.Usage(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "broker_unreachable", err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ---- directory browser (admin only) ----

// DirEntry is one selectable directory.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirListing is the response of GET /api/v1/fs/dirs.
type DirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"`
	Roots   []DirEntry `json:"roots"`
	Entries []DirEntry `json:"entries"`
}

func (s *Server) listDirs(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(peerRoleKey{}).(Role)
	if role != RoleAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("directory browser requires admin role"))
		return
	}
	roots := s.browseRoots()
	resp := DirListing{Roots: roots}
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	canon, err := filepath.EvalSymlinks(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("resolve path: %w", err))
		return
	}
	if !s.underBrowsableRoot(canon) {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("path is outside the browsable roots"))
		return
	}
	entries, err := os.ReadDir(canon)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	resp.Path = canon
	if parent := filepath.Dir(canon); parent != canon && s.underBrowsableRoot(parent) {
		resp.Parent = parent
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		resp.Entries = append(resp.Entries, DirEntry{Name: e.Name(), Path: filepath.Join(canon, e.Name())})
	}
	sort.Slice(resp.Entries, func(i, j int) bool { return resp.Entries[i].Name < resp.Entries[j].Name })
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) browseRoots() []DirEntry {
	seen := map[string]bool{}
	var out []DirEntry
	add := func(p string) {
		if p == "" {
			return
		}
		canon, err := filepath.EvalSymlinks(p)
		if err != nil {
			return
		}
		fi, err := os.Stat(canon)
		if err != nil || !fi.IsDir() || seen[canon] {
			return
		}
		seen[canon] = true
		out = append(out, DirEntry{Name: canon, Path: canon})
	}
	add(s.Root)
	for _, r := range s.AllowedRoots {
		add(r)
	}
	add(s.KnowledgeDir)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (s *Server) underBrowsableRoot(p string) bool {
	check := func(root string) bool {
		if root == "" {
			return false
		}
		canon, err := filepath.EvalSymlinks(root)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(canon, p)
		if err != nil {
			return false
		}
		return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}
	if check(s.Root) {
		return true
	}
	for _, r := range s.AllowedRoots {
		if check(r) {
			return true
		}
	}
	return false
}

func (s *Server) listMCP(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agents.ListMCP(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) addMCP(w http.ResponseWriter, r *http.Request) {
	var m model.AgentMCP
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	out, err := s.Agents.AddMCP(r.Context(), r.PathValue("id"), &m, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateMCP(w http.ResponseWriter, r *http.Request) {
	var m model.AgentMCP
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	out, err := s.Agents.UpdateMCP(r.Context(), r.PathValue("id"), r.PathValue("mcp"), &m, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteMCP(w http.ResponseWriter, r *http.Request) {
	if err := s.Agents.DeleteMCP(r.Context(), r.PathValue("id"), r.PathValue("mcp"), actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agents.ListSkills(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) addSkill(w http.ResponseWriter, r *http.Request) {
	var sk model.AgentSkill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	out, err := s.Agents.AddSkill(r.Context(), r.PathValue("id"), &sk, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateSkill(w http.ResponseWriter, r *http.Request) {
	var sk model.AgentSkill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	out, err := s.Agents.UpdateSkill(r.Context(), r.PathValue("id"), r.PathValue("skill"), &sk, actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.Agents.DeleteSkill(r.Context(), r.PathValue("id"), r.PathValue("skill"), actor(r)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
