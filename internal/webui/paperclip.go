package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// paperclipCLI resolves the Paperclip CLI. It is used as a local adapter for
// status and quick actions; the full Paperclip UI is embedded separately.
func (s *Server) paperclipCLI() string {
	if s.cfg.PaperclipCLI != "" {
		return s.cfg.PaperclipCLI
	}
	home, _ := os.UserHomeDir()
	if p := filepath.Join(home, ".local", "bin", "paperclipai"); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("paperclipai"); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *Server) runPaperclip(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	cli := s.paperclipCLI()
	if cli == "" {
		return nil, fmt.Errorf("paperclip CLI not found")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("paperclip: %s", msg)
	}
	return stdout.Bytes(), nil
}

// paperclipAdapter returns the wrapper command and cwd for newly created
// process agents.
func (s *Server) paperclipAdapter() (string, string) {
	cwd, _ := os.Getwd()
	command := s.cfg.PaperclipWrapper
	if command == "" {
		command = filepath.Join(cwd, "deployments", "paperclip", "dockweave-run.sh")
	}
	return command, cwd
}

// ensurePaperclipAgent creates (or updates) a paperclip process agent named
// DW-<dockweaveAgent> whose adapter target is that DockWeave agent.
func (s *Server) ensurePaperclipAgent(ctx context.Context, companyID, dockweaveAgent string) (string, []byte, error) {
	name := "DW-" + dockweaveAgent
	command, cwd := s.paperclipAdapter()
	cfg := map[string]any{
		"command": command, "args": []string{dockweaveAgent},
		"cwd": cwd, "timeoutSec": 300,
	}
	raw, err := s.runPaperclip(ctx, 10*time.Second, "agent", "list", "--company-id", companyID, "--json")
	if err == nil {
		var list []map[string]any
		if json.Unmarshal(raw, &list) == nil {
			for _, a := range list {
				if a["name"] == name {
					id, _ := a["id"].(string)
					payload, _ := json.Marshal(map[string]any{"adapterConfig": cfg})
					out, err := s.runPaperclip(ctx, 30*time.Second,
						"agent", "update", id, "--payload-json", string(payload), "--json")
					return "updated", out, err
				}
			}
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"name": name, "adapterType": "process", "adapterConfig": cfg,
	})
	out, err := s.runPaperclip(ctx, 30*time.Second,
		"agent", "create", "--company-id", companyID, "--payload-json", string(payload), "--json")
	return "created", out, err
}

func (s *Server) paperclipEnsureAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID string `json:"company_id"`
		Agent     string `json:"agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CompanyID == "" || strings.TrimSpace(req.Agent) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company_id and agent are required"})
		return
	}
	action, out, err := s.ensurePaperclipAgent(r.Context(), req.CompanyID, req.Agent)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action": action, "agent": json.RawMessage(out),
	})
}

func (s *Server) paperclipSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID string   `json:"company_id"`
		Agents    []string `json:"agents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CompanyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company_id is required"})
		return
	}
	created, updated := []string{}, []string{}
	var errs []map[string]string
	for _, a := range req.Agents {
		if strings.TrimSpace(a) == "" {
			continue
		}
		action, _, err := s.ensurePaperclipAgent(r.Context(), req.CompanyID, a)
		if err != nil {
			errs = append(errs, map[string]string{"agent": a, "error": err.Error()})
			continue
		}
		if action == "created" {
			created = append(created, "DW-"+a)
		} else {
			updated = append(updated, "DW-"+a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "updated": updated, "errors": errs,
	})
}

// paperclipConfig returns the URL of the original Paperclip UI for the iframe.
func (s *Server) paperclipConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"url": s.cfg.PaperclipURL})
}

// paperclipStatus returns companies plus the selected company's agents/issues.
func (s *Server) paperclipStatus(w http.ResponseWriter, r *http.Request) {
	companies, err := s.runPaperclip(r.Context(), 10*time.Second, "company", "list", "--json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	companyID := r.URL.Query().Get("company")
	if companyID == "" {
		var list []struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(companies, &list)
		if len(list) > 0 {
			companyID = list[0].ID
		}
	}
	out := map[string]any{
		"url":        s.cfg.PaperclipURL,
		"company_id": companyID,
		"companies":  json.RawMessage(companies),
	}
	if companyID != "" {
		agents, err := s.runPaperclip(r.Context(), 10*time.Second, "agent", "list", "--company-id", companyID, "--json")
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		issues, err := s.runPaperclip(r.Context(), 10*time.Second, "issue", "list", "-C", companyID, "--json")
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		out["agents"] = json.RawMessage(agents)
		out["issues"] = json.RawMessage(issues)
	}
	writeJSON(w, http.StatusOK, out)
}

// paperclipCreateIssue creates a task and optionally assigns it to an agent.
func (s *Server) paperclipCreateIssue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID       string `json:"company_id"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		AssigneeAgentID string `json:"assignee_agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.CompanyID == "" || strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company_id and title are required"})
		return
	}
	args := []string{"issue", "create", "-C", req.CompanyID, "--title", req.Title, "--standalone", "--json"}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}
	if req.AssigneeAgentID != "" {
		args = append(args, "--assignee-agent-id", req.AssigneeAgentID)
	}
	out, err := s.runPaperclip(r.Context(), 30*time.Second, args...)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, json.RawMessage(out))
}

// paperclipSetTarget changes the DockWeave agent (model) a Paperclip process
// agent delegates to by replacing adapterConfig.args[0].
func (s *Server) paperclipSetTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Model) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model is required"})
		return
	}
	agentID := r.PathValue("id")
	raw, err := s.runPaperclip(r.Context(), 10*time.Second, "agent", "get", agentID, "--json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var agent map[string]any
	if err := json.Unmarshal(raw, &agent); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid agent json"})
		return
	}
	cfg, _ := agent["adapterConfig"].(map[string]any)
	if cfg == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent has no adapterConfig"})
		return
	}
	args, _ := cfg["args"].([]any)
	if len(args) == 0 {
		args = []any{req.Model}
	} else {
		args[0] = req.Model
	}
	cfg["args"] = args
	payload, _ := json.Marshal(map[string]any{"adapterConfig": cfg})
	out, err := s.runPaperclip(r.Context(), 30*time.Second,
		"agent", "update", agentID, "--payload-json", string(payload), "--json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(out))
}

// paperclipWake requests a heartbeat run for an agent.
func (s *Server) paperclipWake(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID string `json:"company_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CompanyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company_id is required"})
		return
	}
	out, err := s.runPaperclip(r.Context(), 30*time.Second,
		"agent", "wake", r.PathValue("id"), "-C", req.CompanyID,
		"--source", "assignment", "--trigger", "manual", "--reason", "DockWeave UI", "--json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(out))
}
