// Package model defines the core domain types of DockWeave.
// These types are the stable contract between persistence, API and CLI.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// NewID returns a random, stable identifier with a type prefix, e.g. "ag_9f2c...".
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// ValidateName enforces DNS-label-like names safe for containers and paths.
func ValidateName(kind, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: must match %s", kind, name, nameRe)
	}
	return nil
}

// AgentState is the desired/observed lifecycle state of an agent.
type AgentState string

const (
	AgentCreated AgentState = "created"
	AgentRunning AgentState = "running"
	AgentStopped AgentState = "stopped"
	AgentExited  AgentState = "exited"
	AgentError   AgentState = "error"
)

// NetworkProfile controls enforced egress behaviour for an agent.
// Empty is legacy bridge behaviour for backwards compatibility.
type NetworkProfile string

const (
	NetProfileOffline    NetworkProfile = "offline"
	NetProfileBrokerOnly NetworkProfile = "broker-only"
	NetProfileRestricted NetworkProfile = "restricted-development"
	NetProfileLegacy     NetworkProfile = "unrestricted-legacy"
)

// ValidNetworkProfile reports whether p is a known profile (empty allowed).
func ValidNetworkProfile(p NetworkProfile) bool {
	switch p {
	case "", NetProfileOffline, NetProfileBrokerOnly, NetProfileRestricted, NetProfileLegacy:
		return true
	}
	return false
}

// Agent is the stable identity of an AI coding agent. It is independent of
// container IDs, images, workspaces and memory assignments.
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// DisplayName, OrgUnit and Role are optional organization metadata used by
	// the web graph. They never affect container behavior.
	DisplayName string   `json:"display_name,omitempty"`
	OrgUnit     string   `json:"org_unit,omitempty"`
	Role        string   `json:"role,omitempty"`
	Type        string   `json:"type"` // codex|opencode|claude|gemini|custom
	Image       string   `json:"image"`
	Command     []string `json:"command,omitempty"`
	Env         []string `json:"env,omitempty"` // KEY=VALUE, secrets via EnvFile only
	EnvFile     string   `json:"env_file,omitempty"`
	AgentRO     bool     `json:"agent_ro"` // mount /agent read-only
	UID         int      `json:"uid"`
	GID         int      `json:"gid"`
	AgentDir    string   `json:"agent_dir,omitempty"` // custom /agent source; empty = default tree
	NotesDir    string   `json:"notes_dir,omitempty"` // custom /notes source; empty = default tree
	CPULimit    float64  `json:"cpu_limit,omitempty"` // cores, 0 = unlimited
	MemoryMB    int64    `json:"memory_mb,omitempty"` // 0 = unlimited
	NetworkMode string   `json:"network_mode"`        // bridge|none|host(never default)
	// NetworkProfile controls enforced egress (see docs/egress-security.md).
	NetworkProfile NetworkProfile `json:"network_profile,omitempty"`
	State          AgentState     `json:"state"`
	ContainerID    string         `json:"container_id,omitempty"` // current container, may change
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Validate performs structural validation of an Agent definition.
func (a *Agent) Validate() error {
	if err := ValidateName("agent", a.Name); err != nil {
		return err
	}
	if a.Image == "" {
		return errors.New("agent image must not be empty")
	}
	switch a.NetworkMode {
	case "", "bridge", "none":
	default:
		return fmt.Errorf("unsupported network mode %q (allowed: bridge, none)", a.NetworkMode)
	}
	if a.UID < 0 || a.GID < 0 {
		return errors.New("uid/gid must not be negative")
	}
	if !ValidNetworkProfile(a.NetworkProfile) {
		return fmt.Errorf("unsupported network profile %q", a.NetworkProfile)
	}
	return nil
}

// AccessMode is the mount access mode of a grant.
type AccessMode string

const (
	ModeRO AccessMode = "ro"
	ModeRW AccessMode = "rw"
)

// Workspace is a registered, shareable host directory.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	HostPath  string    `json:"host_path"` // canonical (symlink-resolved) absolute path
	CreatedAt time.Time `json:"created_at"`
}

// Validate performs structural validation of a Workspace definition.
func (w *Workspace) Validate() error {
	if err := ValidateName("workspace", w.Name); err != nil {
		return err
	}
	if w.HostPath == "" {
		return errors.New("workspace host_path must not be empty")
	}
	return nil
}

// Memory is the identity of a long-term memory store, deliberately
// decoupled from agent identities (review-009): it can be attached to a
// replacement agent without losing data, and it has exactly one write
// owner at a time — a second writer is rejected (AGENTS rule 6).
type Memory struct {
	ID           string     `json:"id"`
	OwnerAgentID string     `json:"owner_agent_id,omitempty"` // exactly one writer; empty = unbound
	Path         string     `json:"path,omitempty"`           // custom store dir; empty = <root>/memories/<id>
	CreatedAt    time.Time  `json:"created_at"`
	AttachedAt   *time.Time `json:"attached_at,omitempty"`
}

// GrantStatus is the lifecycle state of a grant. A grant is only *active*
// (permission in effect) after its kernel mount has been applied; every
// intermediate state is visible and resolvable (see todo/review-2026-10.json
// review-002).
type GrantStatus string

const (
	GrantMounting      GrantStatus = "mounting"       // creation in progress (crash-recoverable)
	GrantActive        GrantStatus = "active"         // mount applied, permission live
	GrantMountFailed   GrantStatus = "mount_failed"   // creation failed, cleanup pending
	GrantRevoking      GrantStatus = "revoking"       // revoke in progress
	GrantUnmountFailed GrantStatus = "unmount_failed" // revoke retryable, mount may still be live
	GrantRevoked       GrantStatus = "revoked"        // terminal
)

// Grant authorizes one agent to access one workspace with a mode.
// The grant is the *intent*; the actual mount state lives in MountState.
type Grant struct {
	ID          string      `json:"id"`
	WorkspaceID string      `json:"workspace_id"`
	AgentID     string      `json:"agent_id"`
	Mode        AccessMode  `json:"mode"`
	Exclusive   bool        `json:"exclusive"` // exclusive write (enforced on grant creation)
	Status      GrantStatus `json:"status"`
	ExpiresAt   *time.Time  `json:"expires_at,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	CreatedBy   string      `json:"created_by"` // peer user or "api"
	RevokedAt   *time.Time  `json:"revoked_at,omitempty"`
}

// Expired reports whether the grant has a set expiry in the past.
func (g *Grant) Expired(now time.Time) bool {
	return g.ExpiresAt != nil && now.After(*g.ExpiresAt)
}

// Active reports whether the grant is in effect: its mount is applied and it
// is neither revoked nor expired.
func (g *Grant) Active(now time.Time) bool {
	return g.Status == GrantActive && g.RevokedAt == nil && !g.Expired(now)
}

// Pending reports whether the grant still claims its (workspace, agent) pair:
// it is neither revoked nor terminally failed. Mounting, active, revoking and
// unmount_failed all hold the claim (used for conflict detection).
func (g *Grant) Pending(now time.Time) bool {
	if g.RevokedAt != nil || g.Expired(now) {
		return false
	}
	switch g.Status {
	case GrantMounting, GrantActive, GrantRevoking, GrantUnmountFailed:
		return true
	}
	return false
}

// MountStatus describes the reconciliation state of a single mount.
type MountStatus string

const (
	MountDesired MountStatus = "desired" // should be mounted
	MountActive  MountStatus = "mounted" // verified in kernel mount table
	MountFailed  MountStatus = "failed"
	MountGone    MountStatus = "gone" // removed intentionally
)

// MountState records one bind mount under an agent's /workspace tree.
type MountState struct {
	ID         string      `json:"id"`
	AgentID    string      `json:"agent_id"`
	GrantID    string      `json:"grant_id"`
	HostPath   string      `json:"host_path"`   // canonical source
	TargetPath string      `json:"target_path"` // host-side target under agent workspace tree
	Mode       AccessMode  `json:"mode"`
	Status     MountStatus `json:"status"`
	LastError  string      `json:"last_error,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// EventType classifies audit/runtime events.
type EventType string

const (
	EventAgentCreated   EventType = "agent.created"
	EventAgentStarted   EventType = "agent.started"
	EventAgentStopped   EventType = "agent.stopped"
	EventAgentIdentity  EventType = "agent.identity" // container uid policy (review-005)
	EventAgentRemoved   EventType = "agent.removed"
	EventWorkspaceAdded EventType = "workspace.registered"
	EventWorkspaceGone  EventType = "workspace.removed"
	EventMemoryAttached EventType = "memory.attached"
	EventMemoryDetached EventType = "memory.detached"
	EventGrantCreated   EventType = "grant.created"
	EventGrantRevoked   EventType = "grant.revoked"
	EventGrantRevoking  EventType = "grant.revoking"
	EventMountAdded     EventType = "mount.added"
	EventMountRemoved   EventType = "mount.removed"
	EventMountFailed    EventType = "mount.failed"
	EventUnmountFailed  EventType = "mount.unmount_failed"
	EventUnmountForced  EventType = "mount.unmount_forced" // audited MNT_DETACH (force flag)
	EventRecovery       EventType = "recovery"
	EventSecurityDeny   EventType = "security.denied"
	EventConflict       EventType = "monitor.conflict"
)

// Event is an audit-log record. Events form a hash chain (PrevHash/Hash)
// so tampering with stored history is detectable.
type Event struct {
	ID          string    `json:"id"`
	Type        EventType `json:"type"`
	Actor       string    `json:"actor"` // peer username or "daemon"
	AgentID     string    `json:"agent_id,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Message     string    `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	PrevHash    string    `json:"prev_hash"`
	Hash        string    `json:"hash"`
}

// ActivityLevel is the observed activity of an agent, derived from real
// process/fd data by the monitor. Never reported without evidence.
type ActivityLevel string

const (
	ActivityUnknown ActivityLevel = "UNKNOWN"
	ActivityIdle    ActivityLevel = "IDLE"
	ActivityReading ActivityLevel = "READING"
	ActivityWriting ActivityLevel = "WRITING"
)

// AgentActivity is the monitor's observed snapshot for one agent.
type AgentActivity struct {
	AgentID     string        `json:"agent_id"`
	State       string        `json:"state"`    // docker container state or "absent"
	Activity    ActivityLevel `json:"activity"` // evidence-based
	CPUPercent  float64       `json:"cpu_percent"`
	MemoryBytes uint64        `json:"memory_bytes"`
	Workspace   string        `json:"workspace,omitempty"` // observed primary project
	OpenFiles   []string      `json:"open_files,omitempty"`
	// FileEvidence maps observed host paths to their evidence strength
	// ("kernel" = kernel-reported path, "heuristic" = cwd/args inference).
	FileEvidence map[string]string `json:"file_evidence,omitempty"`
	Processes    int               `json:"processes"`
	Available    bool              `json:"available"` // false => data source unavailable
	Unavailable  string            `json:"unavailable_reason,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
}

// FileConflict reports multiple agents holding the same file open,
// at least one of them for writing.
type FileConflict struct {
	Path     string    `json:"path"`
	AgentIDs []string  `json:"agent_ids"`
	Writers  []string  `json:"writers"`
	Detected time.Time `json:"detected_at"`
}

// MCPTransport is the transport of an MCP server.
type MCPTransport string

const (
	MCPStdio MCPTransport = "stdio"
	MCPHTTP  MCPTransport = "http"
	MCPSse   MCPTransport = "sse"
)

// AgentMCP is a per-agent MCP server configuration. Secrets are never stored
// in EnvKeys (only names); actual values belong in the credential vault.
type AgentMCP struct {
	ID        string       `json:"id"`
	AgentID   string       `json:"agent_id"`
	Name      string       `json:"name"`
	Transport MCPTransport `json:"transport"`
	Command   string       `json:"command,omitempty"`
	Args      []string     `json:"args,omitempty"`
	URL       string       `json:"url,omitempty"`
	EnvKeys   []string     `json:"env_keys,omitempty"`
	Contract  string       `json:"contract,omitempty"`
	Enabled   bool         `json:"enabled"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// AgentSkill is a per-agent skill entry. SourcePath points at an operator-
// provided skill folder; Config carries optional non-secret metadata.
type AgentSkill struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SourcePath  string    `json:"source_path,omitempty"`
	Config      string    `json:"config,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AgentTypeMCP is an MCP server default for an agent type (opencode, claude,
// codex, ...). Per-agent entries override defaults with the same name.
type AgentTypeMCP struct {
	ID        string       `json:"id"`
	AgentType string       `json:"agent_type"`
	Name      string       `json:"name"`
	Transport MCPTransport `json:"transport"`
	Command   string       `json:"command,omitempty"`
	Args      []string     `json:"args,omitempty"`
	URL       string       `json:"url,omitempty"`
	EnvKeys   []string     `json:"env_keys,omitempty"`
	Contract  string       `json:"contract,omitempty"`
	Enabled   bool         `json:"enabled"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// AgentTypeSkill is a skill default for an agent type.
type AgentTypeSkill struct {
	ID          string    `json:"id"`
	AgentType   string    `json:"agent_type"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SourcePath  string    `json:"source_path,omitempty"`
	Config      string    `json:"config,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
