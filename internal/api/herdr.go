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
	"github.com/ananta888/dockweave/internal/model"
)

type herdrCLIEnvelope struct {
	ID     string `json:"id"`
	Result struct {
		Agents []map[string]any `json:"agents"`
		Panes  []map[string]any `json:"panes"`
	} `json:"result"`
}

func (s *Server) herdrAgent(w http.ResponseWriter, r *http.Request) (*model.Agent, bool) {
	a, err := s.Agents.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return nil, false
	}
	if a.Runtime != model.RuntimeHerdr {
		writeErr(w, http.StatusBadRequest, "invalid", fmt.Errorf("agent %q does not use the herdr runtime", a.Name))
		return nil, false
	}
	return a, true
}

func (s *Server) herdrStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.herdrAgent(w, r)
	if !ok {
		return
	}
	cid, err := s.Agents.Docker.FindByAgent(r.Context(), a.ID)
	if err != nil || cid == "" {
		writeErr(w, http.StatusConflict, "not_running", fmt.Errorf("agent %q is not running", a.Name))
		return
	}
	run := func(sub ...string) (herdrCLIEnvelope, error) {
		cmd := append([]string{"env", "HOME=/memory", "/usr/local/bin/herdr"}, sub...)
		out, err := s.Agents.Docker.ExecOutput(r.Context(), cid, cmd)
		if err != nil {
			return herdrCLIEnvelope{}, fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
		}
		var env herdrCLIEnvelope
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			return herdrCLIEnvelope{}, fmt.Errorf("parse herdr output: %w", err)
		}
		return env, nil
	}
	agentsEnv, err := run("agent", "list")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "herdr_error", err)
		return
	}
	panesEnv, err := run("pane", "list")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "herdr_error", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":  a.Name,
		"agents": agentsEnv.Result.Agents,
		"panes":  panesEnv.Result.Panes,
	})
}

func (s *Server) herdrPrompt(w http.ResponseWriter, r *http.Request) {
	a, ok := s.herdrAgent(w, r)
	if !ok {
		return
	}
	if s.Native == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", errAgentSessionsUnavailable)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errNoPrompt)
		return
	}
	if !s.admitPipelineWork(w, r, a) {
		return
	}
	content, err := s.Native.Prompt(r.Context(), a.Type, string(a.Runtime), a.ID, req.Prompt)
	if err != nil {
		s.openAIFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

// herdrPrepare sets up the Codex broker config and a short-lived session token
// inside a herdr agent's memory. It never returns the token.
func (s *Server) herdrPrepare(w http.ResponseWriter, r *http.Request) {
	a, ok := s.herdrAgent(w, r)
	if !ok {
		return
	}
	if a.Type != "codex" {
		writeErr(w, http.StatusBadRequest, "invalid", errors.New("herdr prepare currently supports codex agents"))
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
		req.Credential = "codex-main"
	}
	if req.Model == "" {
		req.Model = "gpt-6.1-sol"
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
			Agent: agent, Credential: req.Credential, Provider: "codex-chatgpt",
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
	baseURL := fmt.Sprintf("http://%s:8787/p/codex-chatgpt", gateway)
	config := fmt.Sprintf(`model_provider = "dockweave"
model = %q

[model_providers.dockweave]
name = "DockWeave"
base_url = %q
wire_api = "responses"

[model_providers.dockweave.auth]
command = "/bin/sh"
args = ["-c", "cat /memory/.config/dockweave/codex-token"]
refresh_interval_ms = 300000
`, req.Model, baseURL)
	configPath := filepath.Join(paths.MemoryDir, ".codex", "config.toml")
	tokenPath := filepath.Join(paths.MemoryDir, ".config", "dockweave", "codex-token")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		s.fail(w, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		s.fail(w, err)
		return
	}
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		s.fail(w, err)
		return
	}
	if err := os.WriteFile(tokenPath, []byte(mint.Token), 0o600); err != nil {
		s.fail(w, err)
		return
	}
	_ = os.Chown(configPath, a.UID, a.GID)
	_ = os.Chown(tokenPath, a.UID, a.GID)
	_ = os.Chown(filepath.Dir(tokenPath), a.UID, a.GID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"grant_id":   grant.ID,
		"expires_at": mint.Session.ExpiresAt.Format(time.RFC3339),
		"config":     configPath,
		"token_file": tokenPath,
	})
}
