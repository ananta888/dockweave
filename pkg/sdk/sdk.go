// Package sdk is the official Go client for the DockWeave management API.
// It speaks HTTP over the daemon's unix socket and is used by the CLI and TUI.
package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Client calls the DockWeave API.
type Client struct {
	hc   *http.Client
	base string // optional http base URL (tests, proxies)
}

// New creates a client for the daemon socket at socketPath.
func New(socketPath string) *Client {
	hc := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	return &Client{hc: hc}
}

// NewHTTP creates a client for a plain HTTP base URL (used by tests and
// by deployments that front the daemon with an authenticated proxy).
func NewHTTP(baseURL string) *Client {
	return &Client{hc: &http.Client{Timeout: 60 * time.Second}, base: strings.TrimSuffix(baseURL, "/")}
}

// APIError mirrors the daemon's structured error.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func (c *Client) url(path string) string {
	if c.base != "" {
		return c.base + path
	}
	return "http://dockweaved" + path
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var eb struct {
			Error APIError `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&eb); err != nil {
			return fmt.Errorf("daemon returned %s", resp.Status)
		}
		return &eb.Error
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Health pings the daemon.
func (c *Client) Health(ctx context.Context) error {
	var v map[string]any
	return c.do(ctx, "GET", "/api/v1/health", nil, &v)
}

// AgentSpec is the creation payload for an agent.
type AgentSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Image       string   `json:"image"`
	Command     []string `json:"command,omitempty"`
	EnvFile     string   `json:"env_file,omitempty"`
	AgentRO     *bool    `json:"agent_ro,omitempty"`
	UID         int      `json:"uid,omitempty"`
	GID         int      `json:"gid,omitempty"`
	CPULimit    float64  `json:"cpu_limit,omitempty"`
	MemoryMB    int64    `json:"memory_mb,omitempty"`
	NetworkMode string   `json:"network_mode,omitempty"`
}

// CreateAgent creates an agent identity.
func (c *Client) CreateAgent(ctx context.Context, spec AgentSpec) (*model.Agent, error) {
	var a model.Agent
	if err := c.do(ctx, "POST", "/api/v1/agents", spec, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAgents lists all agents.
func (c *Client) ListAgents(ctx context.Context) ([]*model.Agent, error) {
	var out []*model.Agent
	if err := c.do(ctx, "GET", "/api/v1/agents", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAgent resolves one agent.
func (c *Client) GetAgent(ctx context.Context, id string) (*model.Agent, error) {
	var a model.Agent
	if err := c.do(ctx, "GET", "/api/v1/agents/"+id, nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// AgentAction triggers start/restart.
func (c *Client) AgentAction(ctx context.Context, id, action string) (*model.Agent, error) {
	var a model.Agent
	if err := c.do(ctx, "POST", "/api/v1/agents/"+id+"/"+action, map[string]string{}, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// StopAgent stops an agent.
func (c *Client) StopAgent(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/api/v1/agents/"+id+"/stop", map[string]string{}, nil)
}

// RemoveAgent deletes an agent (prune removes host data; force allows a
// lazily-detaching unmount of busy mounts during prune — audited).
func (c *Client) RemoveAgent(ctx context.Context, id string, prune, force bool) error {
	q := url.Values{}
	if prune {
		q.Set("prune", "true")
	}
	if force {
		q.Set("force", "true")
	}
	suffix := ""
	if enc := q.Encode(); enc != "" {
		suffix = "?" + enc
	}
	return c.do(ctx, "DELETE", "/api/v1/agents/"+id+suffix, nil, nil)
}

// RegisterWorkspace registers a workspace.
func (c *Client) RegisterWorkspace(ctx context.Context, name, path string) (*model.Workspace, error) {
	var w model.Workspace
	err := c.do(ctx, "POST", "/api/v1/workspaces", map[string]string{"name": name, "path": path}, &w)
	return &w, err
}

// ListWorkspaces lists workspaces.
func (c *Client) ListWorkspaces(ctx context.Context) ([]*model.Workspace, error) {
	var out []*model.Workspace
	if err := c.do(ctx, "GET", "/api/v1/workspaces", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UnregisterWorkspace removes a workspace.
func (c *Client) UnregisterWorkspace(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/workspaces/"+id, nil, nil)
}

// Grant creates a grant.
func (c *Client) Grant(ctx context.Context, workspace, agent, mode string, exclusive bool, ttl string) (*model.Grant, error) {
	var g model.Grant
	err := c.do(ctx, "POST", "/api/v1/grants", map[string]any{
		"workspace": workspace, "agent": agent, "mode": mode, "exclusive": exclusive, "ttl": ttl,
	}, &g)
	return &g, err
}

// ListGrants lists grants.
func (c *Client) ListGrants(ctx context.Context) ([]*model.Grant, error) {
	var out []*model.Grant
	if err := c.do(ctx, "GET", "/api/v1/grants", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeGrant revokes a grant by ID.
func (c *Client) RevokeGrant(ctx context.Context, id string, force bool) error {
	q := ""
	if force {
		q = "?force=true"
	}
	return c.do(ctx, "DELETE", "/api/v1/grants/"+id+q, nil, nil)
}

// ListMounts lists mount states.
func (c *Client) ListMounts(ctx context.Context, agent string) ([]*model.MountState, error) {
	var out []*model.MountState
	q := ""
	if agent != "" {
		q = "?agent=" + agent
	}
	if err := c.do(ctx, "GET", "/api/v1/mounts"+q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Activity returns the live activity snapshot.
func (c *Client) Activity(ctx context.Context) ([]*model.AgentActivity, error) {
	var out []*model.AgentActivity
	if err := c.do(ctx, "GET", "/api/v1/activity", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Conflicts returns current file conflicts.
func (c *Client) Conflicts(ctx context.Context) ([]*model.FileConflict, error) {
	var out []*model.FileConflict
	if err := c.do(ctx, "GET", "/api/v1/conflicts", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Events returns recent events.
func (c *Client) Events(ctx context.Context, limit int) ([]*model.Event, error) {
	var out []*model.Event
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/events?limit=%d", limit), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Providers returns monitoring provider availability.
func (c *Client) Providers(ctx context.Context) (map[string]string, error) {
	var out map[string]string
	if err := c.do(ctx, "GET", "/api/v1/monitor/providers", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- memory snapshots ----

// SnapshotInfo describes a memory snapshot archive (API shape).
type SnapshotInfo struct {
	MemoryID  string `json:"memory_id"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

// MemoryInfo describes a decoupled memory identity (review-009).
type MemoryInfo struct {
	ID           string `json:"id"`
	OwnerAgentID string `json:"owner_agent_id,omitempty"`
	CreatedAt    string `json:"created_at"`
	AttachedAt   string `json:"attached_at,omitempty"`
}

// AgentPathsInfo describes an agent's folder assignments (API shape).
type AgentPathsInfo struct {
	AgentID      string `json:"agent_id"`
	AgentDir     string `json:"agent_dir"`
	MemoryID     string `json:"memory_id,omitempty"`
	MemoryDir    string `json:"memory_dir"`
	WorkspaceDir string `json:"workspace_dir"`
}

// AgentPaths resolves which host directories back /agent, /memory and
// /workspace of an agent.
func (c *Client) AgentPaths(ctx context.Context, idOrName string) (*AgentPathsInfo, error) {
	var out AgentPathsInfo
	if err := c.do(ctx, "GET", "/api/v1/agents/"+idOrName+"/paths", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMemories lists all memory identities and their write owners.
func (c *Client) ListMemories(ctx context.Context) ([]MemoryInfo, error) {
	var out []MemoryInfo
	if err := c.do(ctx, "GET", "/api/v1/memories", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AttachMemory binds a memory to an agent as its single writer.
func (c *Client) AttachMemory(ctx context.Context, agentID, memoryID string) (*MemoryInfo, error) {
	var out MemoryInfo
	if err := c.do(ctx, "POST", "/api/v1/agents/"+agentID+"/memory/attach",
		map[string]string{"memory_id": memoryID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DetachMemory unbinds the agent's memory (data is kept).
func (c *Client) DetachMemory(ctx context.Context, agentID string) (*MemoryInfo, error) {
	var out MemoryInfo
	if err := c.do(ctx, "POST", "/api/v1/agents/"+agentID+"/memory/detach", map[string]string{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SnapshotMemory triggers a memory snapshot for an agent.
func (c *Client) SnapshotMemory(ctx context.Context, agentID string) (*SnapshotInfo, error) {
	var out SnapshotInfo
	if err := c.do(ctx, "POST", "/api/v1/agents/"+agentID+"/memory/snapshot", map[string]string{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMemorySnapshots lists an agent's snapshots.
func (c *Client) ListMemorySnapshots(ctx context.Context, agentID string) ([]SnapshotInfo, error) {
	var out []SnapshotInfo
	if err := c.do(ctx, "GET", "/api/v1/agents/"+agentID+"/memory/snapshots", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RestoreMemorySnapshot restores a snapshot into an agent's memory.
func (c *Client) RestoreMemorySnapshot(ctx context.Context, agentID, snapshot string) error {
	return c.do(ctx, "POST", "/api/v1/agents/"+agentID+"/memory/restore",
		map[string]string{"snapshot": snapshot}, nil)
}

// DeleteMemorySnapshot deletes one snapshot.
func (c *Client) DeleteMemorySnapshot(ctx context.Context, agentID, name string) error {
	return c.do(ctx, "DELETE", "/api/v1/agents/"+agentID+"/memory/snapshots/"+name, nil, nil)
}

// SessionInfo is the adapter-reported session metadata (API shape).
type SessionInfo struct {
	Adapter       string            `json:"adapter"`
	Detected      bool              `json:"detected"`
	ActiveProject string            `json:"active_project,omitempty"`
	Sessions      []string          `json:"sessions,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
}

// AgentSession fetches optional adapter session metadata.
func (c *Client) AgentSession(ctx context.Context, agentID string) (*SessionInfo, error) {
	var out SessionInfo
	if err := c.do(ctx, "GET", "/api/v1/agents/"+agentID+"/session", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitMonitorReport submits an AI-monitor analysis report to the
// restricted monitoring API (the only write operation it may perform).
func (c *Client) SubmitMonitorReport(ctx context.Context, rep any) error {
	return c.do(ctx, "POST", "/api/v1/monitor/reports", rep, nil)
}

// StreamEvents subscribes to the SSE event stream and invokes fn per event.
// It returns when ctx is cancelled or the stream ends.
func (c *Client) StreamEvents(ctx context.Context, fn func(*model.Event)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://dockweaved/api/v1/events/stream", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("event stream: %w", err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	var data string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		} else if line == "" && data != "" {
			var e model.Event
			if json.Unmarshal([]byte(data), &e) == nil {
				fn(&e)
			}
			data = ""
		}
	}
	return sc.Err()
}
