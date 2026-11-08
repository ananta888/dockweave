package credentialbroker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/credentialvault"
)

// AdminConfig configures the operator-facing management API. AllowedUIDs
// are the only peers allowed to mutate secrets, grants and sessions; an
// empty list defaults to the broker process UID.
type AdminConfig struct {
	Vault       *credentialvault.Store
	Policy      *Policy
	Sessions    *SessionStore
	Budget      *Budget
	Broker      *Server
	Providers   map[string]Provider
	AllowedUIDs []uint32
}

type adminServer struct {
	cfg AdminConfig
}

// NewAdminHandler returns the management HTTP handler.
func NewAdminHandler(cfg AdminConfig) (http.Handler, error) {
	if cfg.Vault == nil || cfg.Policy == nil || cfg.Sessions == nil || cfg.Budget == nil {
		return nil, errors.New("vault, policy, sessions and budget are required")
	}
	if len(cfg.AllowedUIDs) == 0 {
		cfg.AllowedUIDs = []uint32{uint32(os.Geteuid())}
	}
	if cfg.Providers == nil {
		cfg.Providers = DefaultProviders()
	}
	return &adminServer{cfg: cfg}, nil
}

func (s *adminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, ok := peerUID(r.Context())
	if !ok || !s.uidAllowed(uid) {
		writeError(w, http.StatusForbidden, "peer not authorized")
		return
	}
	p := r.URL.Path
	switch {
	case p == "/v1/status" && r.Method == http.MethodGet:
		s.status(w)
	case p == "/v1/secrets" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Vault.List())
	case strings.HasPrefix(p, "/v1/secrets/") && r.Method == http.MethodPut:
		s.putSecret(w, r, strings.TrimPrefix(p, "/v1/secrets/"))
	case strings.HasPrefix(p, "/v1/secrets/") && r.Method == http.MethodDelete:
		s.deleteSecret(w, strings.TrimPrefix(p, "/v1/secrets/"))
	case p == "/v1/grants" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Policy.List(r.URL.Query().Get("agent")))
	case p == "/v1/grants" && r.Method == http.MethodPost:
		s.addGrant(w, r)
	case strings.HasPrefix(p, "/v1/grants/") && r.Method == http.MethodDelete:
		if err := s.cfg.Policy.Remove(strings.TrimPrefix(p, "/v1/grants/"), ""); err != nil {
			writeError(w, http.StatusNotFound, "grant not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	case p == "/v1/sessions" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Sessions.InfoList(r.URL.Query().Get("agent")))
	case p == "/v1/sessions" && r.Method == http.MethodPost:
		s.mintSession(w, r)
	case strings.HasPrefix(p, "/v1/sessions/") && r.Method == http.MethodDelete:
		if err := s.cfg.Sessions.Revoke(strings.TrimPrefix(p, "/v1/sessions/")); err != nil {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	case p == "/v1/requests" && r.Method == http.MethodGet:
		if s.cfg.Broker == nil {
			writeJSON(w, http.StatusOK, []RequestLogEntry{})
			return
		}
		writeJSON(w, http.StatusOK, s.cfg.Broker.RecentRequests())
	case p == "/v1/usage" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Budget.Snapshot(r.URL.Query().Get("grant")))
	default:
		writeError(w, http.StatusNotFound, "unknown admin endpoint")
	}
}

func (s *adminServer) uidAllowed(uid uint32) bool {
	for _, u := range s.cfg.AllowedUIDs {
		if u == uid {
			return true
		}
	}
	return false
}

func (s *adminServer) status(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"secrets":   len(s.cfg.Vault.List()),
		"grants":    len(s.cfg.Policy.List("")),
		"sessions":  len(s.cfg.Sessions.List("")),
		"providers": SortedProviders(s.cfg.Providers),
		"egress":    "not enforced (see docs/egress-security.md)",
	})
}

func (s *adminServer) putSecret(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Provider string `json:"provider"`
		Value    string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if name == "" || req.Provider == "" || req.Value == "" {
		writeError(w, http.StatusBadRequest, "name, provider and value are required")
		return
	}
	if _, ok := s.cfg.Providers[req.Provider]; !ok {
		writeError(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if err := s.cfg.Vault.Put(name, req.Provider, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "store failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stored", "name": name})
}

func (s *adminServer) deleteSecret(w http.ResponseWriter, name string) {
	if err := s.cfg.Vault.Delete(name); err != nil {
		writeError(w, http.StatusNotFound, "secret not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *adminServer) addGrant(w http.ResponseWriter, r *http.Request) {
	var g Grant
	if err := decodeJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if g.Provider == "" {
		sec, err := s.cfg.Vault.Get(g.Credential)
		if err != nil {
			writeError(w, http.StatusBadRequest, "unknown credential")
			return
		}
		g.Provider = sec.Provider
	}
	if _, ok := s.cfg.Providers[g.Provider]; !ok {
		writeError(w, http.StatusBadRequest, "unknown provider")
		return
	}
	out, err := s.cfg.Policy.Add(g)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *adminServer) mintSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent      string `json:"agent"`
		GrantID    string `json:"grant_id"`
		Credential string `json:"credential"`
		TTL        string `json:"ttl"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ttl := time.Hour
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ttl")
			return
		}
		ttl = d
	}
	var grant Grant
	if req.GrantID != "" {
		g, ok := s.cfg.Policy.Get(req.GrantID)
		if !ok {
			writeError(w, http.StatusNotFound, "grant not found")
			return
		}
		grant = g
	} else {
		for _, g := range s.cfg.Policy.List(req.Agent) {
			if g.Credential == req.Credential {
				grant = g
				break
			}
		}
		if grant.ID == "" {
			writeError(w, http.StatusNotFound, "grant not found")
			return
		}
	}
	if grant.Agent != req.Agent && req.Agent != "" {
		writeError(w, http.StatusForbidden, "grant belongs to another agent")
		return
	}
	if grant.Expired(time.Now()) {
		writeError(w, http.StatusForbidden, "grant expired")
		return
	}
	token, sess, err := s.cfg.Sessions.Mint(grant.Agent, grant.ID, ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The raw token is returned exactly once and never persisted.
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"session": map[string]any{
			"id": sess.ID, "agent": sess.Agent, "grant_id": sess.GrantID,
			"expires_at": sess.ExpiresAt,
		},
	})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// PeerConnContext resolves SO_PEERCRED on unix connections.
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
	return context.WithValue(ctx, peerUIDKey{}, cred.Uid)
}

type peerUIDKey struct{}

func peerUID(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(peerUIDKey{}).(uint32)
	return v, ok
}
