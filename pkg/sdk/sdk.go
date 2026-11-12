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

	"github.com/ananta888/dockweave/internal/configio"
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
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Image          string   `json:"image"`
	Command        []string `json:"command,omitempty"`
	EnvFile        string   `json:"env_file,omitempty"`
	AgentRO        *bool    `json:"agent_ro,omitempty"`
	UID            int      `json:"uid,omitempty"`
	GID            int      `json:"gid,omitempty"`
	CPULimit       float64  `json:"cpu_limit,omitempty"`
	MemoryMB       int64    `json:"memory_mb,omitempty"`
	NetworkMode    string   `json:"network_mode,omitempty"`
	NetworkProfile string   `json:"network_profile,omitempty"`
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
	Path         string `json:"path,omitempty"`
	CreatedAt    string `json:"created_at"`
	AttachedAt   string `json:"attached_at,omitempty"`
}

// AgentPathsInfo describes an agent's folder assignments (API shape).
type AgentPathsInfo struct {
	AgentID        string `json:"agent_id"`
	AgentDir       string `json:"agent_dir"`
	AgentDirCustom bool   `json:"agent_dir_custom"`
	MemoryID       string `json:"memory_id,omitempty"`
	MemoryDir      string `json:"memory_dir"`
	MemoryCustom   bool   `json:"memory_custom"`
	WorkspaceDir   string `json:"workspace_dir"`
	KnowledgeDir   string `json:"knowledge_dir"`
	NotesDir       string `json:"notes_dir"`
	NotesDirCustom bool   `json:"notes_dir_custom"`
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

// UpdateAgent changes the mutable agent configuration (type, image,
// command, uid/gid). Takes effect at the next start.
func (c *Client) UpdateAgent(ctx context.Context, idOrName, type_, image string, cmd []string, uid, gid int) (*model.Agent, error) {
	body := map[string]any{"type": type_, "image": image, "command": cmd}
	if uid >= 0 {
		body["uid"] = uid
	}
	if gid >= 0 {
		body["gid"] = gid
	}
	var out model.Agent
	if err := c.do(ctx, "PATCH", "/api/v1/agents/"+idOrName, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetAgentDir registers a custom host folder as the /agent source
// (settings, AGENTS.md, skills); empty restores the default.
func (c *Client) SetAgentDir(ctx context.Context, idOrName, path string) (*model.Agent, error) {
	var out model.Agent
	if err := c.do(ctx, "PUT", "/api/v1/agents/"+idOrName+"/agent-dir",
		map[string]string{"path": path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetNotesDir registers a custom host folder as the per-agent knowledge
// folder (/notes); empty restores the default.
func (c *Client) SetNotesDir(ctx context.Context, idOrName, path string) (*model.Agent, error) {
	var out model.Agent
	if err := c.do(ctx, "PUT", "/api/v1/agents/"+idOrName+"/notes-dir",
		map[string]string{"path": path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetEnvFile sets (or clears, with an empty path) the agent env file used at
// start. It is used by the credential migration flow.
func (c *Client) SetEnvFile(ctx context.Context, idOrName, path string) (*model.Agent, error) {
	var out model.Agent
	if err := c.do(ctx, "PUT", "/api/v1/agents/"+idOrName+"/env-file",
		map[string]string{"path": path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetAgentOrganization updates optional organization metadata (display name,
// unit, role) used by the web graph.
func (c *Client) SetAgentOrganization(ctx context.Context, idOrName, displayName, unit, role string) (*model.Agent, error) {
	var out model.Agent
	if err := c.do(ctx, "PUT", "/api/v1/agents/"+idOrName+"/organization",
		map[string]string{"display_name": displayName, "org_unit": unit, "role": role}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetNetworkProfile changes the enforced egress profile (applied at restart).
func (c *Client) SetNetworkProfile(ctx context.Context, idOrName, profile string) (*model.Agent, error) {
	var out model.Agent
	if err := c.do(ctx, "PUT", "/api/v1/agents/"+idOrName+"/network-profile",
		map[string]string{"profile": profile}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EgressStatusInfo is the operator-facing egress view of an agent.
type EgressStatusInfo struct {
	Agent         string `json:"agent"`
	Profile       string `json:"profile"`
	Enforced      bool   `json:"enforced"`
	Network       string `json:"network,omitempty"`
	Gateway       string `json:"gateway,omitempty"`
	BrokerBaseURL string `json:"broker_base_url,omitempty"`
	Warning       string `json:"warning,omitempty"`
}

// EgressStatus reports the configured profile and enforcement state.
func (c *Client) EgressStatus(ctx context.Context, idOrName string) (*EgressStatusInfo, error) {
	var out EgressStatusInfo
	if err := c.do(ctx, "GET", "/api/v1/agents/"+idOrName+"/egress", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RegisterMemory registers a host folder as a long-term memory store.
func (c *Client) RegisterMemory(ctx context.Context, path string) (*MemoryInfo, error) {
	var out MemoryInfo
	if err := c.do(ctx, "POST", "/api/v1/memories", map[string]string{"path": path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExportConfig exports the complete operator configuration as a JSON
// bundle (agents, workspaces, grants, memory bindings — no logs/runtime).
func (c *Client) ExportConfig(ctx context.Context) (*configio.Bundle, error) {
	var out configio.Bundle
	if err := c.do(ctx, "GET", "/api/v1/config/export", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImportConfig recreates a configuration on this deployment (missing
// entities are created, existing ones skipped).
func (c *Client) ImportConfig(ctx context.Context, b *configio.Bundle) (*configio.ImportResult, error) {
	var out configio.ImportResult
	if err := c.do(ctx, "POST", "/api/v1/config/import", b, &out); err != nil {
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

// ---- broker metadata (read-only, never secret values) ----

// BrokerStatus is the broker health/status document.
type BrokerStatus struct {
	Status    string   `json:"status"`
	Secrets   int      `json:"secrets"`
	Grants    int      `json:"grants"`
	Sessions  int      `json:"sessions"`
	Providers []string `json:"providers"`
	Egress    string   `json:"egress"`
}

// BrokerLimits mirrors the broker grant limits needed for display.
type BrokerLimits struct {
	MaxRequestsPerMinute int     `json:"max_requests_per_minute,omitempty"`
	MaxConcurrent        int     `json:"max_concurrent,omitempty"`
	MaxRequestsPerDay    int     `json:"max_requests_per_day,omitempty"`
	MaxRequestsPerMonth  int     `json:"max_requests_per_month,omitempty"`
	MaxTokensPerDay      int64   `json:"max_tokens_per_day,omitempty"`
	MaxTokensPerMonth    int64   `json:"max_tokens_per_month,omitempty"`
	MaxCostUSDPerDay     float64 `json:"max_cost_usd_per_day,omitempty"`
	MaxCostUSDPerMonth   float64 `json:"max_cost_usd_per_month,omitempty"`
}

// BrokerGrant is a credential grant metadata record.
type BrokerGrant struct {
	ID         string       `json:"id"`
	Agent      string       `json:"agent"`
	Credential string       `json:"credential"`
	Provider   string       `json:"provider"`
	Models     []string     `json:"models,omitempty"`
	Limits     BrokerLimits `json:"limits"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	ExpiresAt  *time.Time   `json:"expires_at,omitempty"`
}

// BrokerSessionInfo is a secret-free broker session record.
type BrokerSessionInfo struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`
	GrantID   string     `json:"grant_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// BrokerRequestEntry is a secret-free broker request log record.
type BrokerRequestEntry struct {
	Time             time.Time `json:"time"`
	Agent            string    `json:"agent"`
	Provider         string    `json:"provider"`
	Credential       string    `json:"credential,omitempty"`
	Method           string    `json:"method"`
	Path             string    `json:"path"`
	Model            string    `json:"model,omitempty"`
	Status           int       `json:"status"`
	Denied           bool      `json:"denied,omitempty"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	DurationMS       int64     `json:"duration_ms"`
}

// BrokerUsage is a budget snapshot.
type BrokerUsage struct {
	GrantID       string  `json:"grant_id"`
	Day           string  `json:"day"`
	Month         string  `json:"month"`
	RequestsDay   int     `json:"requests_day"`
	RequestsMonth int     `json:"requests_month"`
	TokensDay     int64   `json:"tokens_day"`
	TokensMonth   int64   `json:"tokens_month"`
	CostDayUSD    float64 `json:"cost_day_usd"`
	CostMonthUSD  float64 `json:"cost_month_usd"`
}

func (c *Client) BrokerStatus(ctx context.Context) (*BrokerStatus, error) {
	var out BrokerStatus
	return &out, c.do(ctx, "GET", "/api/v1/broker/status", nil, &out)
}

func (c *Client) BrokerGrants(ctx context.Context, agent string) ([]BrokerGrant, error) {
	var out []BrokerGrant
	p := "/api/v1/broker/grants"
	if agent != "" {
		p += "?agent=" + url.QueryEscape(agent)
	}
	return out, c.do(ctx, "GET", p, nil, &out)
}

func (c *Client) BrokerSessions(ctx context.Context, agent string) ([]BrokerSessionInfo, error) {
	var out []BrokerSessionInfo
	p := "/api/v1/broker/sessions"
	if agent != "" {
		p += "?agent=" + url.QueryEscape(agent)
	}
	return out, c.do(ctx, "GET", p, nil, &out)
}

func (c *Client) BrokerRequests(ctx context.Context) ([]BrokerRequestEntry, error) {
	var out []BrokerRequestEntry
	return out, c.do(ctx, "GET", "/api/v1/broker/requests", nil, &out)
}

func (c *Client) BrokerUsage(ctx context.Context, grantID string) (*BrokerUsage, error) {
	var out BrokerUsage
	return &out, c.do(ctx, "GET", "/api/v1/broker/usage?grant="+url.QueryEscape(grantID), nil, &out)
}

// DirEntry is one selectable directory in the admin directory browser.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirListing is the admin directory browser response.
type DirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"`
	Roots   []DirEntry `json:"roots"`
	Entries []DirEntry `json:"entries"`
}

// ListDirs lists allowed roots (path empty) or direct subdirectories of a
// path below those roots. Admin-only.
func (c *Client) ListDirs(ctx context.Context, path string) (*DirListing, error) {
	var out DirListing
	p := "/api/v1/fs/dirs"
	if path != "" {
		p += "?path=" + url.QueryEscape(path)
	}
	return &out, c.do(ctx, "GET", p, nil, &out)
}

// ---- per-agent MCP servers and skills ----

// AgentMCP is a per-agent MCP server configuration.
type AgentMCP = model.AgentMCP

// AgentSkill is a per-agent skill entry.
type AgentSkill = model.AgentSkill

func (c *Client) ListMCP(ctx context.Context, agent string) ([]*AgentMCP, error) {
	var out []*AgentMCP
	return out, c.do(ctx, "GET", "/api/v1/agents/"+agent+"/mcp", nil, &out)
}

func (c *Client) AddMCP(ctx context.Context, agent string, m *AgentMCP) (*AgentMCP, error) {
	var out AgentMCP
	return &out, c.do(ctx, "POST", "/api/v1/agents/"+agent+"/mcp", m, &out)
}

func (c *Client) UpdateMCP(ctx context.Context, agent, id string, m *AgentMCP) (*AgentMCP, error) {
	var out AgentMCP
	return &out, c.do(ctx, "PATCH", "/api/v1/agents/"+agent+"/mcp/"+url.PathEscape(id), m, &out)
}

func (c *Client) DeleteMCP(ctx context.Context, agent, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/agents/"+agent+"/mcp/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListSkills(ctx context.Context, agent string) ([]*AgentSkill, error) {
	var out []*AgentSkill
	return out, c.do(ctx, "GET", "/api/v1/agents/"+agent+"/skills", nil, &out)
}

func (c *Client) AddSkill(ctx context.Context, agent string, sk *AgentSkill) (*AgentSkill, error) {
	var out AgentSkill
	return &out, c.do(ctx, "POST", "/api/v1/agents/"+agent+"/skills", sk, &out)
}

func (c *Client) UpdateSkill(ctx context.Context, agent, id string, sk *AgentSkill) (*AgentSkill, error) {
	var out AgentSkill
	return &out, c.do(ctx, "PATCH", "/api/v1/agents/"+agent+"/skills/"+url.PathEscape(id), sk, &out)
}

func (c *Client) DeleteSkill(ctx context.Context, agent, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/agents/"+agent+"/skills/"+url.PathEscape(id), nil, nil)
}

// ---- agent-type defaults ----

type AgentTypeMCP = model.AgentTypeMCP
type AgentTypeSkill = model.AgentTypeSkill

func (c *Client) ListTypeMCP(ctx context.Context, agentType string) ([]*AgentTypeMCP, error) {
	var out []*AgentTypeMCP
	return out, c.do(ctx, "GET", "/api/v1/agent-types/"+agentType+"/mcp", nil, &out)
}

func (c *Client) AddTypeMCP(ctx context.Context, agentType string, m *AgentTypeMCP) (*AgentTypeMCP, error) {
	var out AgentTypeMCP
	return &out, c.do(ctx, "POST", "/api/v1/agent-types/"+agentType+"/mcp", m, &out)
}

func (c *Client) UpdateTypeMCP(ctx context.Context, agentType, id string, m *AgentTypeMCP) (*AgentTypeMCP, error) {
	var out AgentTypeMCP
	return &out, c.do(ctx, "PATCH", "/api/v1/agent-types/"+agentType+"/mcp/"+url.PathEscape(id), m, &out)
}

func (c *Client) DeleteTypeMCP(ctx context.Context, agentType, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/agent-types/"+agentType+"/mcp/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListTypeSkills(ctx context.Context, agentType string) ([]*AgentTypeSkill, error) {
	var out []*AgentTypeSkill
	return out, c.do(ctx, "GET", "/api/v1/agent-types/"+agentType+"/skills", nil, &out)
}

func (c *Client) AddTypeSkill(ctx context.Context, agentType string, sk *AgentTypeSkill) (*AgentTypeSkill, error) {
	var out AgentTypeSkill
	return &out, c.do(ctx, "POST", "/api/v1/agent-types/"+agentType+"/skills", sk, &out)
}

func (c *Client) UpdateTypeSkill(ctx context.Context, agentType, id string, sk *AgentTypeSkill) (*AgentTypeSkill, error) {
	var out AgentTypeSkill
	return &out, c.do(ctx, "PATCH", "/api/v1/agent-types/"+agentType+"/skills/"+url.PathEscape(id), sk, &out)
}

func (c *Client) DeleteTypeSkill(ctx context.Context, agentType, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/agent-types/"+agentType+"/skills/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ImportTypeDefaults(ctx context.Context, agentType string) (map[string]int, error) {
	var out map[string]int
	return out, c.do(ctx, "POST", "/api/v1/agent-types/"+agentType+"/import", map[string]string{}, &out)
}
