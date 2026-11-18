package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/credentialbroker"
)

// enableDeepSeek writes an OpenAI-compatible provider block into an opencode
// agent's config. The agent receives only a short-lived dws_ session token;
// the real DeepSeek key stays in the vault.
func (s *Server) enableDeepSeek(w http.ResponseWriter, r *http.Request) {
	a, err := s.Agents.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if a.Type != "opencode" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("DeepSeek provider activation supports opencode agents"))
		return
	}
	if s.Broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "broker_unavailable",
			errors.New("credential broker admin socket is not configured"))
		return
	}
	var req struct {
		Credential string `json:"credential"`
		Model      string `json:"model"`
		TTL        string `json:"ttl"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Credential == "" {
		req.Credential = "deepseek-main"
	}
	if req.Model == "" {
		req.Model = "deepseek-flash"
	}
	if req.TTL == "" {
		req.TTL = "8h"
	}

	agent := a.Name
	grants, err := s.Broker.ListGrants(r.Context(), agent)
	if err != nil {
		s.fail(w, err)
		return
	}
	var grant *credentialbroker.Grant
	for i := range grants {
		if grants[i].Credential == req.Credential {
			grant = &grants[i]
			break
		}
	}
	if grant == nil {
		grant, err = s.Broker.AddGrant(r.Context(), credentialbroker.Grant{
			Agent: agent, Credential: req.Credential, Provider: "deepseek",
			Models: []string{"*"}, Limits: credentialbroker.DefaultLimits(),
		})
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	mint, err := s.Broker.MintSession(r.Context(), agent, grant.ID, req.Credential, req.TTL)
	if err != nil {
		s.fail(w, err)
		return
	}

	paths, err := s.Agents.AgentPaths(r.Context(), a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	gateway, err := s.Agents.Docker.NetworkGateway(r.Context(), "bridge")
	if err != nil || gateway == "" {
		gateway = "172.17.0.1"
	}
	baseURL := OpenCodeBaseURL(gateway)
	providerName := "dockweave-deepseek"

	configPath := filepath.Join(paths.MemoryDir, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		s.fail(w, err)
		return
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[providerName] = map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "DeepSeek via DockWeave",
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  mint.Token,
		},
		"models": map[string]any{
			"deepseek-flash":  map[string]any{"name": "DeepSeek Flash"},
			"deepseek-v4-pro": map[string]any{"name": "DeepSeek V4 Pro"},
		},
	}
	cfg["provider"] = providers
	cfg["model"] = providerName + "/" + req.Model
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		s.fail(w, err)
		return
	}
	_ = os.Chown(configPath, a.UID, a.GID)
	_ = os.Chown(filepath.Dir(configPath), a.UID, a.GID)

	// Best effort: tell the running OpenCode server to reload its config.
	if cid, err := s.Agents.Docker.FindByAgent(r.Context(), a.ID); err == nil && cid != "" {
		_, _ = s.Agents.Docker.ExecOutput(r.Context(), cid, []string{
			"env", "HOME=/memory", "/memory/.local/bin/opencode", "reload",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"provider":   providerName,
		"model":      req.Model,
		"session_id": mint.Session.ID,
		"expires_at": mint.Session.ExpiresAt.Format(time.RFC3339),
		"config":     configPath,
	})
}

// OpenCodeBaseURL is exported for tests/documentation.
func OpenCodeBaseURL(gateway string) string {
	if strings.TrimSpace(gateway) == "" {
		gateway = "172.17.0.1"
	}
	return fmt.Sprintf("http://%s:8787/p/deepseek", gateway)
}
