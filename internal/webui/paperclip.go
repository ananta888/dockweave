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
