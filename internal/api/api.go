// Package api implements the versioned DockWeave management API (v1),
// served over a Unix domain socket with SO_PEERCRED authorization.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/user"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/agents"
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
	case errors.Is(err, persistence.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, workspaces.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, mounts.ErrBusy):
		return http.StatusConflict, "busy"
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

type peerKey struct{}

// PeerAuthMiddleware injects the SO_PEERCRED-derived username for unix
// socket connections. TCP listeners (if ever enabled) must supply their own
// auth layer before this handler.
func PeerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(peerKey{}) == nil {
			r = r.WithContext(context.WithValue(r.Context(), peerKey{}, "unknown"))
		}
		next.ServeHTTP(w, r)
	})
}

// PeerConnContext implements http.Server.ConnContext for unix listeners,
// resolving the peer credentials of each connection.
func PeerConnContext(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return ctx
	}
	var cred *unix.Ucred
	_ = raw.Control(func(fd uintptr) {
		cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || cred == nil {
		return ctx
	}
	name := fmt.Sprintf("uid:%d", cred.Uid)
	if u, err := user.LookupId(strconv.Itoa(int(cred.Uid))); err == nil {
		name = u.Username
	}
	return context.WithValue(ctx, peerKey{}, name)
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
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Image       string   `json:"image"`
	Command     []string `json:"command"`
	EnvFile     string   `json:"env_file"`
	AgentRO     *bool    `json:"agent_ro"`
	UID         int      `json:"uid"`
	GID         int      `json:"gid"`
	CPULimit    float64  `json:"cpu_limit"`
	MemoryMB    int64    `json:"memory_mb"`
	NetworkMode string   `json:"network_mode"`
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
	if err := s.Agents.Remove(r.Context(), r.PathValue("id"), actor(r), prune); err != nil {
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

// fail maps service errors to structured API errors.
func (s *Server) fail(w http.ResponseWriter, err error) {
	st, code := classify(err)
	writeErr(w, st, code, err)
}
