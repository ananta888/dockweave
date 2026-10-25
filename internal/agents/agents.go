// Package agents implements the agent lifecycle on top of the registry,
// the mount manager and the Docker client. It is the service layer shared
// by the API (and therefore CLI and TUI).
package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentadapters "github.com/ananta888/dockweave/integrations/agents"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/memory"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// MountOps abstracts the privileged mount manager (enables unit tests
// without CAP_SYS_ADMIN). *mounts.Manager implements it.
type MountOps interface {
	PrepareAgentTree(agentID string) error
	AgentDir(agentID string) string
	MemoryDir(agentID string) string
	WorkspaceTree(agentID string) string
	List(agentID string) ([]model.MountState, error)
	Remove(agentID, targetName string, force bool) error
	TeardownAgentTree(agentID string, force bool) (detached []string, err error)
	ChownAgentTree(agentID string, uid, gid int) error
	ValidateSource(path string) (string, error)
}

// DockerOps abstracts the Docker engine client (enables unit tests).
// *dockerclient.Client implements it.
type DockerOps interface {
	EnsureImage(ctx context.Context, ref string, out io.Writer) error
	Create(ctx context.Context, spec dockerclient.ContainerSpec) (string, error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string, timeoutSec int) error
	Remove(ctx context.Context, containerID string, force bool) error
	FindByAgent(ctx context.Context, agentID string) (string, error)
	State(ctx context.Context, containerID string) (string, error)
	ExecOutput(ctx context.Context, containerID string, cmd []string) (string, error)
}

// Service manages agent identities and their containers.
type Service struct {
	Store  *persistence.Store
	Mounts MountOps
	Docker DockerOps
	// Memory performs snapshot/restore on agent memory directories.
	// Optional: if nil, memory operations return an error.
	Memory *memory.Manager
	// DefaultUID/DefaultGID is the container identity assigned to agents
	// without an explicit UID (review-005: non-root containers by default).
	DefaultUID, DefaultGID int
	// AllowRootContainer permits UID 0 containers; every root container is
	// audited. Root is refused otherwise and the default identity applied.
	AllowRootContainer bool
	// KnowledgeDir is the shared human-readable knowledge store mounted
	// at /knowledge in every agent (empty: no knowledge mount).
	KnowledgeDir string
	// Grants revokes grants during agent removal (usually *workspaces.Service).
	// Required before removing agents that hold grants.
	Grants GrantRevoker
}

// ErrMemoryUnavailable is returned when memory snapshotting is not configured.
var ErrMemoryUnavailable = errors.New("memory snapshotting not configured on daemon")

// ErrMemoryInUse is returned when a memory attach/detach would violate the
// single-writer rule (AGENTS rule 6).
var ErrMemoryInUse = errors.New("memory already has a write owner")

// boundMemory returns the memory identity currently owned by the agent.
func (s *Service) boundMemory(ctx context.Context, agentID string) (*model.Memory, error) {
	m, err := s.Store.GetMemoryByAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent %s has no memory attached: %w", agentID, err)
	}
	_ = s.memDir(m) // keep snapshot manager in sync with custom folders
	return m, nil
}

// chownMemory hands a memory store to the container identity.
func (s *Service) chownMemory(a *model.Agent, memID string) error {
	dir := s.Memory.Path(memID)
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, a.UID, a.GID)
	})
	if err != nil {
		return fmt.Errorf("chown memory %s to %d:%d: %w", memID, a.UID, a.GID, err)
	}
	return nil
}

// AttachMemory binds a memory store to an agent as its single writer
// (review-009). Refuses while the agent runs and while the memory has a
// different owner — detach first. Audited.
func (s *Service) AttachMemory(ctx context.Context, idOrName, memoryID, actor string) (*model.Memory, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if err := s.ensureStopped(ctx, a); err != nil {
		return nil, err
	}
	mem, err := s.Store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, fmt.Errorf("memory %q: %w", memoryID, err)
	}
	if mem.OwnerAgentID != "" && mem.OwnerAgentID != a.ID {
		return nil, fmt.Errorf("%w: memory %s is owned by agent %s (detach first)", ErrMemoryInUse, mem.ID, mem.OwnerAgentID)
	}
	if cur, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil && cur.ID != mem.ID {
		return nil, fmt.Errorf("%w: agent %s already owns memory %s (detach first)", ErrMemoryInUse, a.Name, cur.ID)
	}
	if err := s.Store.SetMemoryOwner(ctx, mem.ID, a.ID); err != nil {
		return nil, err
	}
	if s.Memory != nil {
		_ = s.Memory.EnsureDir(mem.ID)
		_ = s.chownMemory(a, mem.ID)
	}
	now := time.Now()
	mem.OwnerAgentID = a.ID
	mem.AttachedAt = &now
	s.event(ctx, model.EventMemoryAttached, actor, a.ID,
		fmt.Sprintf("memory %s attached to agent %q", mem.ID, a.Name))
	return mem, nil
}

// DetachMemory unbinds the agent's memory; the data is kept and the store
// can be attached to a replacement agent later. Refuses while the agent
// runs. Audited.
func (s *Service) DetachMemory(ctx context.Context, idOrName, actor string) (*model.Memory, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if err := s.ensureStopped(ctx, a); err != nil {
		return nil, err
	}
	mem, err := s.Store.GetMemoryByAgent(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("agent %s has no memory attached: %w", a.Name, err)
	}
	if err := s.Store.SetMemoryOwner(ctx, mem.ID, ""); err != nil {
		return nil, err
	}
	mem.OwnerAgentID = ""
	mem.AttachedAt = nil
	s.event(ctx, model.EventMemoryDetached, actor, a.ID,
		fmt.Sprintf("memory %s detached from agent %q", mem.ID, a.Name))
	return mem, nil
}

// ListMemories returns all memory identities and their write owners.
func (s *Service) ListMemories(ctx context.Context) ([]*model.Memory, error) {
	return s.Store.ListMemories(ctx)
}

// ensureStopped refuses memory identity changes while the agent runs
// (a running container holds its /memory mount and may write).
func (s *Service) ensureStopped(ctx context.Context, a *model.Agent) error {
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		return err
	}
	if cid != "" {
		if st, err := s.Docker.State(ctx, cid); err == nil && st == "running" {
			return memory.ErrBusy
		}
	}
	return nil
}

// EnsureMemoryIdentities migrates the legacy per-agent memory directories
// (agents/<id>/memory) to decoupled memory stores (<root>/memories/<id>,
// review-009): every agent receives a memory identity, and a non-empty
// legacy directory is moved wholesale (os.Rename, atomic on the same
// filesystem — no data loss). Idempotent; called at daemon startup.
func (s *Service) EnsureMemoryIdentities(ctx context.Context) error {
	if s.Memory == nil {
		return ErrMemoryUnavailable
	}
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range agents {
		if _, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
			continue // already has an identity
		}
		now := time.Now()
		mem := &model.Memory{
			ID: model.NewID("mem"), OwnerAgentID: a.ID,
			CreatedAt: now, AttachedAt: &now,
		}
		if err := s.Store.CreateMemory(ctx, mem); err != nil {
			errs = append(errs, fmt.Errorf("agent %s: %w", a.Name, err))
			continue
		}
		dst := s.Memory.Path(mem.ID)
		legacy := s.Mounts.MemoryDir(a.ID)
		moved := false
		if fi, err := os.Stat(legacy); err == nil && fi.IsDir() {
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				// os.Rename needs the target's parent to exist; the target
				// itself must not (rename replaces the legacy dir wholesale).
				if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
					errs = append(errs, fmt.Errorf("agent %s: %w", a.Name, err))
				} else if err := os.Rename(legacy, dst); err != nil {
					errs = append(errs, fmt.Errorf("agent %s: migrate memory dir: %w", a.Name, err))
				} else {
					moved = true
				}
			}
		}
		if !moved {
			if err := s.Memory.EnsureDir(mem.ID); err != nil {
				errs = append(errs, fmt.Errorf("agent %s: %w", a.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// SnapshotMemory creates a memory snapshot for the agent's bound memory
// and applies the retention policy afterwards.
func (s *Service) SnapshotMemory(ctx context.Context, idOrName, actor string) (*memory.Snapshot, error) {
	if s.Memory == nil {
		return nil, ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	mem, err := s.boundMemory(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	snap, err := s.Memory.Create(mem.ID)
	if err != nil {
		return nil, err
	}
	// Default retention: keep the 10 newest snapshots.
	if _, err := s.Memory.ApplyRetention(mem.ID, memory.RetentionPolicy{MaxCount: 10}); err != nil {
		// Retention failure does not fail the snapshot; report via event.
		s.event(ctx, "memory.retention_failed", actor, a.ID, "retention after snapshot failed: "+err.Error())
	}
	s.event(ctx, "memory.snapshot", actor, a.ID, "snapshot "+snap.Name)
	return snap, nil
}

// ListMemorySnapshots lists snapshots of the agent's bound memory.
func (s *Service) ListMemorySnapshots(ctx context.Context, idOrName string) ([]memory.Snapshot, error) {
	if s.Memory == nil {
		return nil, ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	mem, err := s.boundMemory(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	return s.Memory.List(mem.ID)
}

// RestoreMemorySnapshot restores a snapshot. Refuses while the agent runs
// (restoring under a live SQLite writer corrupts state).
func (s *Service) RestoreMemorySnapshot(ctx context.Context, idOrName, name, actor string) error {
	if s.Memory == nil {
		return ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	mem, err := s.boundMemory(ctx, a.ID)
	if err != nil {
		return err
	}
	if err := s.ensureStopped(ctx, a); err != nil {
		return err
	}
	if err := s.Memory.Restore(mem.ID, name); err != nil {
		return err
	}
	// Restored files are written by the daemon (root); hand them back to the
	// container identity so the non-root agent can use its memory.
	_ = s.chownMemory(a, mem.ID)
	s.event(ctx, "memory.restore", actor, a.ID, "restored "+name)
	return nil
}

// DeleteMemorySnapshot removes one snapshot (audited).
func (s *Service) DeleteMemorySnapshot(ctx context.Context, idOrName, name, actor string) error {
	if s.Memory == nil {
		return ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	mem, err := s.boundMemory(ctx, a.ID)
	if err != nil {
		return err
	}
	if err := s.Memory.Delete(mem.ID, name); err != nil {
		return err
	}
	s.event(ctx, "memory.snapshot_delete", actor, a.ID, "deleted "+name)
	return nil
}

// SessionInfo probes optional agent adapters (codex/opencode/claude/gemini)
// via docker exec and returns enriched session metadata. Without adapters
// configured or when the container is not running, a not-detected result
// is returned — never fabricated data.
func (s *Service) SessionInfo(ctx context.Context, idOrName string) (*agentadapters.SessionInfo, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if cid == "" {
		return &agentadapters.SessionInfo{Detected: false}, nil
	}
	st, err := s.Docker.State(ctx, cid)
	if err != nil {
		return nil, err
	}
	if st != "running" {
		return &agentadapters.SessionInfo{Detected: false}, nil
	}
	exec := func(ctx context.Context, cmd []string) (string, error) {
		return s.Docker.ExecOutput(ctx, cid, cmd)
	}
	reg := agentadapters.NewRegistry()
	adapter, ok := reg.DetectAll(ctx, exec)
	if !ok {
		return &agentadapters.SessionInfo{Detected: false}, nil
	}
	return adapter.SessionInfo(ctx, exec)
}

// Eventf appends an audit event; failures are logged to stderr implicitly by callers.
func (s *Service) event(ctx context.Context, t model.EventType, actor, agentID, msg string) {
	_ = s.Store.AppendEvent(ctx, &model.Event{
		ID: model.NewID("ev"), Type: t, Actor: actor, AgentID: agentID,
		Message: msg, Timestamp: time.Now(),
	})
}

// Create registers a new agent identity and prepares its host tree.
func (s *Service) Create(ctx context.Context, a *model.Agent, actor string) (*model.Agent, error) {
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("invalid agent: %w", err)
	}
	a.ID = model.NewID("ag")
	now := time.Now()
	a.CreatedAt, a.UpdatedAt = now, now
	a.State = model.AgentCreated
	if a.Type == "" {
		a.Type = "custom"
	}
	s.applyContainerIdentity(ctx, a, actor)
	if err := s.Store.CreateAgent(ctx, a); err != nil {
		return nil, err
	}
	if err := s.Mounts.PrepareAgentTree(a.ID); err != nil {
		// Roll back identity on tree failure to avoid half-created agents.
		_ = s.Store.DeleteAgent(ctx, a.ID)
		return nil, fmt.Errorf("prepare agent tree: %w", err)
	}
	// The non-root container identity needs access to its host tree.
	if err := s.Mounts.ChownAgentTree(a.ID, a.UID, a.GID); err != nil {
		_ = s.Store.DeleteAgent(ctx, a.ID)
		return nil, err
	}
	// Long-term memory gets its own identity with this agent as the single
	// write owner (review-009).
	mem := &model.Memory{
		ID: model.NewID("mem"), OwnerAgentID: a.ID,
		CreatedAt: now, AttachedAt: &now,
	}
	if err := s.Store.CreateMemory(ctx, mem); err != nil {
		_ = s.Store.DeleteAgent(ctx, a.ID)
		return nil, fmt.Errorf("create memory identity: %w", err)
	}
	if s.Memory != nil {
		if err := s.Memory.EnsureDir(mem.ID); err != nil {
			_ = s.Store.DeleteAgent(ctx, a.ID)
			return nil, err
		}
		if err := s.chownMemory(a, mem.ID); err != nil {
			_ = s.Store.DeleteAgent(ctx, a.ID)
			return nil, err
		}
	}
	s.event(ctx, model.EventAgentCreated, actor, a.ID, fmt.Sprintf("agent %q created (image %s, memory %s)", a.Name, a.Image, mem.ID))
	return a, nil
}

// applyContainerIdentity enforces the container identity policy
// (review-005): agents without an explicit UID are assigned the configured
// non-root default. Root (0:0) is only honored when the daemon explicitly
// allows root containers — and audited in both cases.
func (s *Service) applyContainerIdentity(ctx context.Context, a *model.Agent, actor string) {
	if a.UID != 0 {
		return // explicit non-root identity, honored as-is
	}
	if s.AllowRootContainer {
		s.event(ctx, model.EventAgentIdentity, actor, a.ID,
			fmt.Sprintf("agent %q runs as root (0:0) — AllowRootContainer is enabled", a.Name))
		return
	}
	du, dg := s.DefaultUID, s.DefaultGID
	if du == 0 {
		du = 1000
	}
	if dg == 0 {
		dg = 1000
	}
	a.UID, a.GID = du, dg
	s.event(ctx, model.EventAgentIdentity, actor, a.ID,
		fmt.Sprintf("agent %q identity defaulted to %d:%d (root containers not allowed)", a.Name, a.UID, a.GID))
}

// Get resolves an agent by ID or name.
func (s *Service) Get(ctx context.Context, idOrName string) (*model.Agent, error) {
	return s.Store.GetAgent(ctx, idOrName)
}

// AgentPaths describes the folder assignments of an agent container: which
// host directory backs /agent (settings, AGENTS.md, skills), /memory
// (long-term memory) and /workspace (project mounts).
type AgentPaths struct {
	AgentID        string `json:"agent_id"`
	AgentDir       string `json:"agent_dir"` // host dir mounted at /agent
	AgentDirCustom bool   `json:"agent_dir_custom"`
	MemoryID       string `json:"memory_id,omitempty"`
	MemoryDir      string `json:"memory_dir"` // host dir mounted at /memory
	MemoryCustom   bool   `json:"memory_custom"`
	WorkspaceDir   string `json:"workspace_dir"` // host tree mounted at /workspace
	KnowledgeDir   string `json:"knowledge_dir"` // host dir mounted at /knowledge
}

// agentDir resolves the /agent source: the registered custom folder or the
// default tree directory.
func (s *Service) agentDir(a *model.Agent) string {
	if a.AgentDir != "" {
		return a.AgentDir
	}
	return s.Mounts.AgentDir(a.ID)
}

// memDir resolves the /memory source of a memory identity and keeps the
// snapshot manager in sync with registered custom folders.
func (s *Service) memDir(mem *model.Memory) string {
	if s.Memory == nil {
		return ""
	}
	if mem.Path != "" {
		s.Memory.SetPath(mem.ID, mem.Path)
		return mem.Path
	}
	return s.Memory.Path(mem.ID)
}

// AgentPaths resolves the folder assignments of one agent.
func (s *Service) AgentPaths(ctx context.Context, idOrName string) (*AgentPaths, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	p := &AgentPaths{
		AgentID:        a.ID,
		AgentDir:       s.agentDir(a),
		AgentDirCustom: a.AgentDir != "",
		WorkspaceDir:   s.Mounts.WorkspaceTree(a.ID),
		KnowledgeDir:   s.KnowledgeDir,
	}
	if s.Memory != nil {
		if mem, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
			p.MemoryID = mem.ID
			p.MemoryDir = s.memDir(mem)
			p.MemoryCustom = mem.Path != ""
		}
	}
	return p, nil
}

// UpdateAgentConfig changes the mutable configuration of an agent (type,
// image, command, uid/gid). It takes effect at the next agent start.
func (s *Service) UpdateAgentConfig(ctx context.Context, idOrName string, type_, image string, cmd []string, uid, gid int, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if type_ != "" {
		a.Type = type_
	}
	if image != "" {
		a.Image = image
	}
	if cmd != nil {
		a.Command = cmd
	}
	if uid >= 0 {
		a.UID = uid
	}
	if gid >= 0 {
		a.GID = gid
	}
	a.UpdatedAt = time.Now()
	if err := s.Store.UpdateAgentConfig(ctx, a); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID,
		fmt.Sprintf("config updated: type=%s image=%s uid=%d:%d (restart to apply)", a.Type, a.Image, a.UID, a.GID))
	return a, nil
}

// SetAgentDir registers a custom host directory as the /agent source
// (settings, AGENTS.md, skills). An empty path restores the default. The
// change takes effect at the next agent start.
func (s *Service) SetAgentDir(ctx context.Context, idOrName, path, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	dir := ""
	if strings.TrimSpace(path) != "" {
		if dir, err = s.Mounts.ValidateSource(path); err != nil {
			return nil, err
		}
	}
	if err := s.Store.UpdateAgentDir(ctx, a.ID, dir); err != nil {
		return nil, err
	}
	a.AgentDir = dir
	msg := "agent folder reset to default"
	if dir != "" {
		msg = "agent folder set to " + dir
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, msg+" (restart to apply)")
	return a, nil
}

// RegisterMemory registers a host directory as a long-term memory store.
// The memory is created unbound; attach it to an agent to make it the
// single writer.
func (s *Service) RegisterMemory(ctx context.Context, path, actor string) (*model.Memory, error) {
	if s.Memory == nil {
		return nil, ErrMemoryUnavailable
	}
	canon, err := s.Mounts.ValidateSource(path)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	mem := &model.Memory{ID: model.NewID("mem"), Path: canon, CreatedAt: now}
	if err := s.Store.CreateMemory(ctx, mem); err != nil {
		return nil, err
	}
	s.Memory.SetPath(mem.ID, canon)
	s.event(ctx, model.EventMemoryAttached, actor, "",
		fmt.Sprintf("memory folder registered: %s (%s)", mem.ID, canon))
	return mem, nil
}

// List returns all agents.
func (s *Service) List(ctx context.Context) ([]*model.Agent, error) {
	return s.Store.ListAgents(ctx)
}

// Start creates or reuses the container and starts it. Idempotent.
func (s *Service) Start(ctx context.Context, idOrName, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	// Load env file secrets into the runtime spec (never persisted).
	env, err := a.Env, error(nil)
	if a.EnvFile != "" {
		b, err := os.ReadFile(a.EnvFile)
		if err != nil {
			return nil, fmt.Errorf("read env file: %w", err)
		}
		for _, line := range splitLines(string(b)) {
			if line != "" && line[0] != '#' {
				env = append(env, line)
			}
		}
	}
	runtimeAgent := *a
	runtimeAgent.Env = env

	if err := s.Docker.EnsureImage(ctx, a.Image, nil); err != nil {
		return nil, err
	}
	// Existing container for this identity?
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if cid != "" {
		st, err := s.Docker.State(ctx, cid)
		if err != nil {
			return nil, err
		}
		if st == "running" {
			_ = s.Store.UpdateAgentState(ctx, a.ID, model.AgentRunning, cid)
			a.State, a.ContainerID = model.AgentRunning, cid
			return a, nil // idempotent
		}
		// Container exists but configuration may be stale (image/env changed):
		// recreate to guarantee the three-mount layout and hardening.
		_ = s.Docker.Remove(ctx, cid, true)
	}
	// Folder assignments (review follow-up): registered custom folders win
	// over the default tree layout; /memory comes from the bound memory
	// store (review-009).
	memPath := s.Mounts.MemoryDir(a.ID)
	if s.Memory != nil {
		if mem, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
			memPath = s.memDir(mem)
		}
	}
	// Boot inside the first active project so the agent finds its
	// AGENTS.md/skills and project files without manual navigation.
	workDir := "/workspace"
	if ms, err := s.Mounts.List(a.ID); err == nil {
		for _, mt := range ms {
			if mt.Status == model.MountActive {
				workDir = "/workspace/" + filepath.Base(mt.TargetPath)
				break
			}
		}
	}
	ncid, err := s.Docker.Create(ctx, dockerclient.ContainerSpec{
		Agent:        &runtimeAgent,
		AgentDir:     s.agentDir(a),
		MemoryDir:    memPath,
		WorkspaceDir: s.Mounts.WorkspaceTree(a.ID),
		KnowledgeDir: s.KnowledgeDir,
		WorkingDir:   workDir,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Docker.Start(ctx, ncid); err != nil {
		_ = s.Docker.Remove(ctx, ncid, true)
		return nil, err
	}
	if err := s.Store.UpdateAgentState(ctx, a.ID, model.AgentRunning, ncid); err != nil {
		return nil, err
	}
	a.State, a.ContainerID = model.AgentRunning, ncid
	s.event(ctx, model.EventAgentStarted, actor, a.ID, "container "+shortID(ncid))
	return a, nil
}

// Stop stops the agent's container. Idempotent.
func (s *Service) Stop(ctx context.Context, idOrName, actor string) error {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		return err
	}
	if cid == "" {
		_ = s.Store.UpdateAgentState(ctx, a.ID, model.AgentStopped, "")
		return nil
	}
	st, err := s.Docker.State(ctx, cid)
	if err == nil && st == "running" {
		if err := s.Docker.Stop(ctx, cid, 10); err != nil {
			return err
		}
	}
	if err := s.Store.UpdateAgentState(ctx, a.ID, model.AgentStopped, cid); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentStopped, actor, a.ID, "")
	return nil
}

// Restart restarts the agent.
func (s *Service) Restart(ctx context.Context, idOrName, actor string) (*model.Agent, error) {
	if err := s.Stop(ctx, idOrName, actor); err != nil {
		return nil, err
	}
	return s.Start(ctx, idOrName, actor)
}

// GrantRevoker revokes grants during agent removal (implemented by
// *workspaces.Service). Agent removal must never leave grants behind:
// their rows block the registry delete and their mounts would be orphaned.
type GrantRevoker interface {
	Revoke(ctx context.Context, grantID, actor string, forceUnmount bool) error
}

// Remove deletes the agent identity and container. Host data (agent install,
// memory) is preserved unless prune is true.
//
// Removal order (safe and retryable): container → grants (revoked, mounts
// removed) → memory binding (detached; pruned with --prune) → host data →
// registry rows. Every error is collected and returned; the agent record is
// deleted only when everything below it is clean.
//
// Prune safety (review-003): MNT_DETACH is only used when force is
// explicitly set (graceful umount is tried first; every detached mount is
// audited).
func (s *Service) Remove(ctx context.Context, idOrName, actor string, prune, force bool) error {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	if cid, err := s.Docker.FindByAgent(ctx, a.ID); err == nil && cid != "" {
		if err := s.Docker.Remove(ctx, cid, true); err != nil {
			return fmt.Errorf("remove container for agent %s: %w", a.ID, err)
		}
	}

	var errs []error

	// 1. Revoke every grant of this agent (unmounts included). Leaving rows
	// behind would block the delete (FK) and orphan live mounts.
	grants, err := s.Store.ListGrants(ctx, "", a.ID, false)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if g.RevokedAt != nil {
			continue
		}
		if s.Grants == nil {
			errs = append(errs, fmt.Errorf("grant %s exists but no grant revoker is configured", g.ID))
			continue
		}
		if err := s.Grants.Revoke(ctx, g.ID, actor, force); err != nil {
			errs = append(errs, fmt.Errorf("revoke grant %s: %w", g.ID, err))
		}
	}

	// 2. Unbind the memory identity; the store survives for a replacement
	// agent unless --prune deletes it below.
	var mem *model.Memory
	if s.Memory != nil {
		if m, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
			mem = m
			if err := s.Store.SetMemoryOwner(ctx, m.ID, ""); err != nil {
				errs = append(errs, fmt.Errorf("detach memory %s: %w", m.ID, err))
			} else {
				s.event(ctx, model.EventMemoryDetached, actor, a.ID,
					fmt.Sprintf("memory %s detached during agent removal", m.ID))
			}
		}
	}

	// 3. Prune host data (mount leftovers, tree, agent root, memory store).
	if prune {
		ms, err := s.Mounts.List(a.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("list mounts: %w", err))
		}
		for i := len(ms) - 1; i >= 0; i-- {
			name := baseName(ms[i].TargetPath)
			if err := s.Mounts.Remove(a.ID, name, false); err != nil {
				if force {
					// Explicit force: audited MNT_DETACH after graceful failure.
					s.event(ctx, model.EventUnmountForced, actor, a.ID,
						fmt.Sprintf("forced detach of %q during prune: %v", name, err))
					if err = s.Mounts.Remove(a.ID, name, true); err != nil {
						errs = append(errs, fmt.Errorf("forced unmount %q: %w", name, err))
						continue
					}
				} else {
					errs = append(errs, fmt.Errorf("unmount %q: %w", name, err))
					continue
				}
			}
		}
		// Tear down the workspace tree self-bind (and any stragglers) so the
		// host data can actually be deleted. Forced detaches are audited.
		detached, terr := s.Mounts.TeardownAgentTree(a.ID, force)
		for _, mp := range detached {
			s.event(ctx, model.EventUnmountForced, actor, a.ID,
				fmt.Sprintf("forced detach of %q during prune teardown", mp))
		}
		if terr != nil {
			errs = append(errs, fmt.Errorf("teardown workspace tree: %w", terr))
		}
		agentRoot := filepath.Dir(s.Mounts.WorkspaceTree(a.ID)) // <root>/agents/<id>
		if filepath.Base(agentRoot) == a.ID {
			if err := os.RemoveAll(agentRoot); err != nil {
				errs = append(errs, fmt.Errorf("remove %q: %w", agentRoot, err))
			}
		}
		if mem != nil {
			if err := os.RemoveAll(s.Memory.Path(mem.ID)); err != nil {
				errs = append(errs, fmt.Errorf("remove memory store %s: %w", mem.ID, err))
			} else if err := s.Store.DeleteMemory(ctx, mem.ID); err != nil {
				errs = append(errs, fmt.Errorf("delete memory %s: %w", mem.ID, err))
			}
		}
	}
	if len(errs) > 0 {
		// Keep the agent record: the removal stays retryable until clean.
		return fmt.Errorf("remove incomplete (agent record kept, retry remove): %w", errors.Join(errs...))
	}

	// 4. Registry rows last (FK-safe: grant rows are gone by now).
	if err := s.Store.PurgeGrantsByAgent(ctx, a.ID); err != nil {
		return err
	}
	if err := s.Store.DeleteAgent(ctx, a.ID); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentRemoved, actor, a.ID,
		fmt.Sprintf("agent %q removed (prune=%v, force=%v)", a.Name, prune, force))
	return nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, trimSpace(s[start:]))
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// ErrAgentRunning is returned when an operation requires a stopped agent.
var ErrAgentRunning = errors.New("agent is running")
