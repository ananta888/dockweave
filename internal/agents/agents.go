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
	"github.com/ananta888/dockweave/internal/agentconfig"
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
	NotesDir(agentID string) string
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
	EnsureNetwork(ctx context.Context, name string, internal bool) (string, error)
	NetworkGateway(ctx context.Context, name string) (string, error)
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
	if a.Runtime == "" {
		a.Runtime = model.RuntimeDocker
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
	// Prefill per-agent MCP/skill rows from type defaults so each agent has its
	// own editable copy (type defaults remain the catalog/template).
	_, _, _ = s.seedAgentDefaults(ctx, a.ID, a.Type)
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
	NotesDir       string `json:"notes_dir"`     // host dir mounted at /notes
	NotesDirCustom bool   `json:"notes_dir_custom"`
}

// agentDir resolves the /agent source: the registered custom folder or the
// default tree directory.
func (s *Service) agentDir(a *model.Agent) string {
	if a.AgentDir != "" {
		return a.AgentDir
	}
	return s.Mounts.AgentDir(a.ID)
}

// notesDir resolves the /notes source: the registered custom folder or the
// default per-agent notes directory.
func (s *Service) notesDir(a *model.Agent) string {
	if a.NotesDir != "" {
		return a.NotesDir
	}
	return s.Mounts.NotesDir(a.ID)
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
		NotesDir:       s.notesDir(a),
		NotesDirCustom: a.NotesDir != "",
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
// image, command, uid/gid, runtime). It takes effect at the next agent start.
func (s *Service) UpdateAgentConfig(ctx context.Context, idOrName string, type_, image string, cmd []string, uid, gid int, runtime, actor string) (*model.Agent, error) {
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
	if runtime != "" {
		if !model.ValidRuntime(model.RuntimeType(runtime)) {
			return nil, fmt.Errorf("unsupported runtime %q (allowed: docker, herdr, paperclip)", runtime)
		}
		a.Runtime = model.RuntimeType(runtime)
	}
	a.UpdatedAt = time.Now()
	if err := s.Store.UpdateAgentConfig(ctx, a); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID,
		fmt.Sprintf("config updated: type=%s image=%s uid=%d:%d runtime=%s (restart to apply)", a.Type, a.Image, a.UID, a.GID, a.Runtime))
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

// SetNotesDir registers a custom host directory as the per-agent
// knowledge folder (/notes). An empty path restores the default. The
// change takes effect at the next agent start.
func (s *Service) SetNotesDir(ctx context.Context, idOrName, path, actor string) (*model.Agent, error) {
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
	if err := s.Store.UpdateNotesDir(ctx, a.ID, dir); err != nil {
		return nil, err
	}
	a.NotesDir = dir
	msg := "notes folder reset to default"
	if dir != "" {
		msg = "notes folder set to " + dir
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, msg+" (restart to apply)")
	return a, nil
}

// SetEnvFile sets (or with an empty path clears) the host env file read at
// start. The credential migration flow clears it only after secrets have
// been moved to the vault; the file itself is never deleted here.
func (s *Service) SetEnvFile(ctx context.Context, idOrName, path, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	envFile := ""
	if strings.TrimSpace(path) != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve env file: %w", err)
		}
		canon, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("canonicalize env file: %w", err)
		}
		fi, err := os.Stat(canon)
		if err != nil {
			return nil, fmt.Errorf("stat env file: %w", err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("%q is not a regular file", canon)
		}
		envFile = canon
	}
	if err := s.Store.UpdateAgentEnvFile(ctx, a.ID, envFile); err != nil {
		return nil, err
	}
	a.EnvFile = envFile
	msg := "env file cleared (restart to apply)"
	if envFile != "" {
		msg = "env file set to " + envFile + " (restart to apply)"
	}
	// Never include file contents, only the path.
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, msg)
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

// ---- per-agent MCP servers and skills ----

// ListMCP returns the MCP servers configured for an agent.
func (s *Service) ListMCP(ctx context.Context, idOrName string) ([]*model.AgentMCP, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	return s.Store.ListAgentMCP(ctx, a.ID)
}

// AddMCP creates an MCP server configuration and materializes it.
func (s *Service) AddMCP(ctx context.Context, idOrName string, m *model.AgentMCP, actor string) (*model.AgentMCP, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if err := validateMCP(m); err != nil {
		return nil, err
	}
	now := time.Now()
	m.ID, m.AgentID = model.NewID("mcp"), a.ID
	m.CreatedAt, m.UpdatedAt = now, now
	if err := s.Store.CreateAgentMCP(ctx, m); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "MCP server added: "+m.Name)
	_ = s.materializeConfig(ctx, a)
	return m, nil
}

// UpdateMCP replaces an MCP server configuration.
func (s *Service) UpdateMCP(ctx context.Context, idOrName, mcpID string, m *model.AgentMCP, actor string) (*model.AgentMCP, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if err := validateMCP(m); err != nil {
		return nil, err
	}
	list, err := s.Store.ListAgentMCP(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	var old *model.AgentMCP
	for _, existing := range list {
		if existing.ID == mcpID || existing.Name == mcpID {
			old = existing
			break
		}
	}
	if old == nil {
		return nil, persistence.ErrNotFound
	}
	m.ID, m.AgentID = old.ID, a.ID
	m.CreatedAt, m.UpdatedAt = old.CreatedAt, time.Now()
	if err := s.Store.UpdateAgentMCP(ctx, m); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "MCP server updated: "+m.Name)
	_ = s.materializeConfig(ctx, a)
	return m, nil
}

// DeleteMCP removes an MCP server configuration.
func (s *Service) DeleteMCP(ctx context.Context, idOrName, mcpID, actor string) error {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAgentMCP(ctx, a.ID, mcpID); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "MCP server removed: "+mcpID)
	_ = s.materializeConfig(ctx, a)
	return nil
}

// ListSkills returns the skills configured for an agent.
func (s *Service) ListSkills(ctx context.Context, idOrName string) ([]*model.AgentSkill, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	return s.Store.ListAgentSkills(ctx, a.ID)
}

// AddSkill creates a skill entry and materializes it.
func (s *Service) AddSkill(ctx context.Context, idOrName string, sk *model.AgentSkill, actor string) (*model.AgentSkill, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sk.Name) == "" {
		return nil, errors.New("skill name is required")
	}
	now := time.Now()
	sk.ID, sk.AgentID = model.NewID("skl"), a.ID
	sk.CreatedAt, sk.UpdatedAt = now, now
	if err := s.Store.CreateAgentSkill(ctx, sk); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "skill added: "+sk.Name)
	_ = s.materializeConfig(ctx, a)
	return sk, nil
}

// UpdateSkill replaces a skill entry.
func (s *Service) UpdateSkill(ctx context.Context, idOrName, skillID string, sk *model.AgentSkill, actor string) (*model.AgentSkill, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sk.Name) == "" {
		return nil, errors.New("skill name is required")
	}
	list, err := s.Store.ListAgentSkills(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	var old *model.AgentSkill
	for _, existing := range list {
		if existing.ID == skillID || existing.Name == skillID {
			old = existing
			break
		}
	}
	if old == nil {
		return nil, persistence.ErrNotFound
	}
	sk.ID, sk.AgentID = old.ID, a.ID
	sk.CreatedAt, sk.UpdatedAt = old.CreatedAt, time.Now()
	if err := s.Store.UpdateAgentSkill(ctx, sk); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "skill updated: "+sk.Name)
	_ = s.materializeConfig(ctx, a)
	return sk, nil
}

// DeleteSkill removes a skill entry.
func (s *Service) DeleteSkill(ctx context.Context, idOrName, skillID, actor string) error {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAgentSkill(ctx, a.ID, skillID); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "skill removed: "+skillID)
	_ = s.materializeConfig(ctx, a)
	return nil
}

func validateMCP(m *model.AgentMCP) error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("MCP server name is required")
	}
	switch m.Transport {
	case model.MCPStdio:
		if m.Command == "" {
			return errors.New("stdio MCP server requires command")
		}
	case model.MCPHTTP, model.MCPSse:
		if m.URL == "" {
			return errors.New("http/sse MCP server requires url")
		}
	default:
		return fmt.Errorf("unsupported MCP transport %q", m.Transport)
	}
	return nil
}

// ApplyTypeDefaults copies missing type-default MCP servers/skills into the
// agent's own rows and materializes the result.
func (s *Service) ApplyTypeDefaults(ctx context.Context, idOrName, actor string) (int, int, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return 0, 0, err
	}
	m, sk, err := s.seedAgentDefaults(ctx, a.ID, a.Type)
	if err != nil {
		return 0, 0, err
	}
	if err := s.materializeConfig(ctx, a); err != nil {
		return m, sk, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, a.ID,
		fmt.Sprintf("type defaults applied: %d MCP, %d skills", m, sk))
	return m, sk, nil
}

func (s *Service) seedAgentDefaults(ctx context.Context, agentID, agentType string) (int, int, error) {
	typeMCP, err := s.Store.ListAgentTypeMCP(ctx, agentType)
	if err != nil {
		return 0, 0, err
	}
	ownMCP, err := s.Store.ListAgentMCP(ctx, agentID)
	if err != nil {
		return 0, 0, err
	}
	haveMCP := map[string]bool{}
	for _, m := range ownMCP {
		haveMCP[m.Name] = true
	}
	typeSkills, err := s.Store.ListAgentTypeSkills(ctx, agentType)
	if err != nil {
		return 0, 0, err
	}
	ownSkills, err := s.Store.ListAgentSkills(ctx, agentID)
	if err != nil {
		return 0, 0, err
	}
	haveSkill := map[string]bool{}
	for _, sk := range ownSkills {
		haveSkill[sk.Name] = true
	}
	now := time.Now()
	addedMCP, addedSkills := 0, 0
	for _, t := range typeMCP {
		if haveMCP[t.Name] {
			continue
		}
		m := &model.AgentMCP{
			ID: model.NewID("mcp"), AgentID: agentID, Name: t.Name, Transport: t.Transport,
			Command: t.Command, Args: t.Args, URL: t.URL, EnvKeys: t.EnvKeys,
			Contract: t.Contract, Enabled: t.Enabled, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.Store.CreateAgentMCP(ctx, m); err == nil {
			addedMCP++
		}
	}
	for _, t := range typeSkills {
		if haveSkill[t.Name] {
			continue
		}
		sk := &model.AgentSkill{
			ID: model.NewID("skl"), AgentID: agentID, Name: t.Name, Description: t.Description,
			SourcePath: t.SourcePath, Config: t.Config, Enabled: t.Enabled, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.Store.CreateAgentSkill(ctx, sk); err == nil {
			addedSkills++
		}
	}
	return addedMCP, addedSkills, nil
}

func (s *Service) materializeConfig(ctx context.Context, a *model.Agent) error {
	memPath := s.Mounts.MemoryDir(a.ID)
	if s.Memory != nil {
		if mem, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
			memPath = s.memDir(mem)
		}
	}
	mcps, err := s.Store.ListAgentMCP(ctx, a.ID)
	if err != nil {
		return err
	}
	skills, err := s.Store.ListAgentSkills(ctx, a.ID)
	if err != nil {
		return err
	}
	typeMCP, err := s.Store.ListAgentTypeMCP(ctx, a.Type)
	if err != nil {
		return err
	}
	typeSkills, err := s.Store.ListAgentTypeSkills(ctx, a.Type)
	if err != nil {
		return err
	}
	return agentconfig.Write(memPath, a.Type, a.UID, a.GID,
		mergeMCP(typeMCP, mcps), mergeSkills(typeSkills, skills))
}

func mergeMCP(defaults []*model.AgentTypeMCP, own []*model.AgentMCP) []*model.AgentMCP {
	out := make([]*model.AgentMCP, 0, len(defaults)+len(own))
	overridden := map[string]bool{}
	for _, o := range own {
		overridden[o.Name] = true
	}
	for _, d := range defaults {
		if overridden[d.Name] {
			continue
		}
		out = append(out, &model.AgentMCP{
			Name: d.Name, Transport: d.Transport, Command: d.Command, Args: d.Args,
			URL: d.URL, EnvKeys: d.EnvKeys, Contract: d.Contract, Enabled: d.Enabled,
		})
	}
	out = append(out, own...)
	return out
}

func mergeSkills(defaults []*model.AgentTypeSkill, own []*model.AgentSkill) []*model.AgentSkill {
	out := make([]*model.AgentSkill, 0, len(defaults)+len(own))
	overridden := map[string]bool{}
	for _, o := range own {
		overridden[o.Name] = true
	}
	for _, d := range defaults {
		if overridden[d.Name] {
			continue
		}
		out = append(out, &model.AgentSkill{
			Name: d.Name, Description: d.Description, SourcePath: d.SourcePath,
			Config: d.Config, Enabled: d.Enabled,
		})
	}
	out = append(out, own...)
	return out
}

// ---- per-agent-type defaults ----

func (s *Service) ListTypeMCP(ctx context.Context, agentType string) ([]*model.AgentTypeMCP, error) {
	return s.Store.ListAgentTypeMCP(ctx, agentType)
}

func (s *Service) AddTypeMCP(ctx context.Context, agentType string, m *model.AgentTypeMCP, actor string) (*model.AgentTypeMCP, error) {
	if err := validateMCP(&model.AgentMCP{Name: m.Name, Transport: m.Transport, Command: m.Command, URL: m.URL}); err != nil {
		return nil, err
	}
	now := time.Now()
	m.ID, m.AgentType = model.NewID("tmcp"), agentType
	m.CreatedAt, m.UpdatedAt = now, now
	if err := s.Store.CreateAgentTypeMCP(ctx, m); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventAgentIdentity, actor, "", "type MCP added: "+agentType+"/"+m.Name)
	return m, nil
}

func (s *Service) UpdateTypeMCP(ctx context.Context, agentType, id string, m *model.AgentTypeMCP, actor string) (*model.AgentTypeMCP, error) {
	if err := validateMCP(&model.AgentMCP{Name: m.Name, Transport: m.Transport, Command: m.Command, URL: m.URL}); err != nil {
		return nil, err
	}
	list, err := s.Store.ListAgentTypeMCP(ctx, agentType)
	if err != nil {
		return nil, err
	}
	var old *model.AgentTypeMCP
	for _, x := range list {
		if x.ID == id || x.Name == id {
			old = x
			break
		}
	}
	if old == nil {
		return nil, persistence.ErrNotFound
	}
	m.ID, m.AgentType = old.ID, agentType
	m.CreatedAt, m.UpdatedAt = old.CreatedAt, time.Now()
	if err := s.Store.UpdateAgentTypeMCP(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) DeleteTypeMCP(ctx context.Context, agentType, id, actor string) error {
	if err := s.Store.DeleteAgentTypeMCP(ctx, agentType, id); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentIdentity, actor, "", "type MCP removed: "+agentType+"/"+id)
	return nil
}

func (s *Service) ListTypeSkills(ctx context.Context, agentType string) ([]*model.AgentTypeSkill, error) {
	return s.Store.ListAgentTypeSkills(ctx, agentType)
}

func (s *Service) AddTypeSkill(ctx context.Context, agentType string, sk *model.AgentTypeSkill, actor string) (*model.AgentTypeSkill, error) {
	if strings.TrimSpace(sk.Name) == "" {
		return nil, errors.New("skill name is required")
	}
	now := time.Now()
	sk.ID, sk.AgentType = model.NewID("tskl"), agentType
	sk.CreatedAt, sk.UpdatedAt = now, now
	if err := s.Store.CreateAgentTypeSkill(ctx, sk); err != nil {
		return nil, err
	}
	return sk, nil
}

func (s *Service) UpdateTypeSkill(ctx context.Context, agentType, id string, sk *model.AgentTypeSkill, actor string) (*model.AgentTypeSkill, error) {
	list, err := s.Store.ListAgentTypeSkills(ctx, agentType)
	if err != nil {
		return nil, err
	}
	var old *model.AgentTypeSkill
	for _, x := range list {
		if x.ID == id || x.Name == id {
			old = x
			break
		}
	}
	if old == nil {
		return nil, persistence.ErrNotFound
	}
	sk.ID, sk.AgentType = old.ID, agentType
	sk.CreatedAt, sk.UpdatedAt = old.CreatedAt, time.Now()
	if err := s.Store.UpdateAgentTypeSkill(ctx, sk); err != nil {
		return nil, err
	}
	return sk, nil
}

func (s *Service) DeleteTypeSkill(ctx context.Context, agentType, id, actor string) error {
	return s.Store.DeleteAgentTypeSkill(ctx, agentType, id)
}

// ImportTypeDefaults scans existing agents of the given type and common host
// homes, then stores discovered skills/MCP servers as type defaults. Existing
// entries are kept (operator edits win).
func (s *Service) ImportTypeDefaults(ctx context.Context, agentType, actor string) (int, int, error) {
	if !validAgentType(agentType) {
		return 0, 0, fmt.Errorf("unsupported agent type %q", agentType)
	}
	dirs := map[string]bool{}
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range agents {
		if a.Type != agentType {
			continue
		}
		memPath := s.Mounts.MemoryDir(a.ID)
		if s.Memory != nil {
			if mem, err := s.Store.GetMemoryByAgent(ctx, a.ID); err == nil {
				memPath = s.memDir(mem)
			}
		}
		dirs[memPath] = true
	}
	homes, _ := filepath.Glob("/home/*")
	for _, h := range homes {
		dirs[h] = true
	}
	if _, err := os.Stat("/root"); err == nil {
		dirs["/root"] = true
	}
	candidates := make([]string, 0, len(dirs))
	for d := range dirs {
		candidates = append(candidates, d)
	}
	mcps, skills := agentconfig.Scan(agentType, candidates)
	existingMCP, _ := s.Store.ListAgentTypeMCP(ctx, agentType)
	haveMCP := map[string]bool{}
	for _, m := range existingMCP {
		haveMCP[m.Name] = true
	}
	existingSkills, _ := s.Store.ListAgentTypeSkills(ctx, agentType)
	haveSkill := map[string]bool{}
	for _, sk := range existingSkills {
		haveSkill[sk.Name] = true
	}
	addedMCP, addedSkills := 0, 0
	now := time.Now()
	for _, m := range mcps {
		if haveMCP[m.Name] {
			continue
		}
		m.ID, m.AgentType = model.NewID("tmcp"), agentType
		m.CreatedAt, m.UpdatedAt = now, now
		if err := s.Store.CreateAgentTypeMCP(ctx, m); err == nil {
			addedMCP++
		}
	}
	for _, sk := range skills {
		if haveSkill[sk.Name] {
			continue
		}
		sk.ID, sk.AgentType = model.NewID("tskl"), agentType
		sk.CreatedAt, sk.UpdatedAt = now, now
		if err := s.Store.CreateAgentTypeSkill(ctx, sk); err == nil {
			addedSkills++
		}
	}
	// Copy new defaults into each existing agent of this type and
	// re-materialize so per-agent rows stay prefilled but editable.
	for _, a := range agents {
		if a.Type == agentType {
			_, _, _ = s.seedAgentDefaults(ctx, a.ID, a.Type)
			_ = s.materializeConfig(ctx, a)
		}
	}
	_ = actor
	return addedMCP, addedSkills, nil
}

func validAgentType(t string) bool {
	switch t {
	case "opencode", "claude", "codex", "gemini", "antigravity", "custom":
		return true
	}
	return false
}

// runtimeCommand returns the container entrypoint for the agent's runtime
// adapter. The container stays the security boundary in every case; the
// runtime only decides who drives the terminal/process inside it.
func runtimeCommand(a *model.Agent) []string {
	switch a.Runtime {
	case model.RuntimeHerdr:
		// herdr owns the terminal inside the container: the harness runs as a
		// herdr pane so sessions can detach/reattach and persist. The image
		// must provide the `herdr` binary. Without an explicit command, herdr
		// starts with its defaults.
		return append([]string{"herdr", "agent", "start", "--kind", a.Type}, a.Command...)
	case model.RuntimePaperclip:
		// paperclip drives the agent in heartbeats; the harness CLI runs
		// normally, marked via env (see applyRuntime) so a paperclip adapter
		// can attach and wake it.
		return a.Command
	default: // docker (or empty)
		return a.Command
	}
}

// applyRuntime adjusts the runtime spec for the agent's runtime adapter.
func (s *Service) applyRuntime(a *model.Agent) {
	a.Command = runtimeCommand(a)
	if a.Runtime == model.RuntimePaperclip {
		a.Env = append(a.Env, "DOCKWEAVE_RUNTIME=paperclip")
	}
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
	if err := s.applyNetworkProfile(ctx, &runtimeAgent); err != nil {
		return nil, err
	}
	s.applyRuntime(&runtimeAgent)

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
	if err := s.materializeConfig(ctx, a); err != nil {
		return nil, fmt.Errorf("materialize agent config: %w", err)
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
		NotesDir:     s.notesDir(a),
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

// EgressStatus is the operator-facing egress view of one agent.
type EgressStatus struct {
	Agent         string               `json:"agent"`
	Profile       model.NetworkProfile `json:"profile"`
	Enforced      bool                 `json:"enforced"`
	Network       string               `json:"network,omitempty"`
	Gateway       string               `json:"gateway,omitempty"`
	BrokerBaseURL string               `json:"broker_base_url,omitempty"`
	Warning       string               `json:"warning,omitempty"`
}

// BrokerProfilePort is the port an operator must bind the broker to on the
// broker-only network gateway.
const BrokerProfilePort = 8787

func (s *Service) applyNetworkProfile(ctx context.Context, a *model.Agent) error {
	switch a.NetworkProfile {
	case "", model.NetProfileLegacy:
		return nil // legacy bridge/none behaviour, explicitly not isolated
	case model.NetProfileOffline:
		a.NetworkMode = "none"
		return nil
	case model.NetProfileBrokerOnly:
		gw, err := s.Docker.EnsureNetwork(ctx, dockerclient.NetworkBrokerOnly, true)
		if err != nil {
			return fmt.Errorf("egress broker-only: %w (refusing to start; fail closed)", err)
		}
		a.NetworkMode = dockerclient.NetworkBrokerOnly
		a.Env = append(a.Env, fmt.Sprintf("DOCKWEAVE_BROKER_BASE_URL=http://%s:%d", gw, BrokerProfilePort))
		return nil
	case model.NetProfileRestricted:
		return errors.New("network profile restricted-development is not implemented; refusing to start (fail closed)")
	default:
		return fmt.Errorf("unsupported network profile %q", a.NetworkProfile)
	}
}

// SetNetworkProfile changes the enforced egress profile. It takes effect at
// the next start/restart; this method never weakens an existing profile
// silently.
func (s *Service) SetNetworkProfile(ctx context.Context, idOrName, profile, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	p := model.NetworkProfile(profile)
	if !model.ValidNetworkProfile(p) {
		return nil, fmt.Errorf("unsupported network profile %q (offline|broker-only|restricted-development|unrestricted-legacy)", profile)
	}
	if err := s.Store.UpdateAgentNetworkProfile(ctx, a.ID, string(p)); err != nil {
		return nil, err
	}
	a.NetworkProfile = p
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "network profile set to "+string(p)+" (restart to apply)")
	return a, nil
}

// SetOrganization updates optional organization metadata used by the web
// graph (display name, unit, role). It never affects container behavior.
func (s *Service) SetOrganization(ctx context.Context, idOrName, displayName, unit, role, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	displayName = strings.TrimSpace(displayName)
	unit = strings.TrimSpace(unit)
	role = strings.TrimSpace(role)
	if err := s.Store.UpdateAgentOrganization(ctx, a.ID, displayName, unit, role); err != nil {
		return nil, err
	}
	a.DisplayName, a.OrgUnit, a.Role = displayName, unit, role
	s.event(ctx, model.EventAgentIdentity, actor, a.ID,
		fmt.Sprintf("organization set: unit=%q role=%q display=%q", unit, role, displayName))
	return a, nil
}

// SetAvatar sets the agent's 2D figure. avatar is a built-in role id; svg is
// optional custom SVG markup (bounded, never executed by the backend).
func (s *Service) SetAvatar(ctx context.Context, idOrName, avatar, svg, actor string) (*model.Agent, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	svg = strings.TrimSpace(svg)
	if len(svg) > 64*1024 {
		return nil, errors.New("avatar SVG exceeds 64 KiB")
	}
	if svg != "" && !strings.HasPrefix(strings.ToLower(svg), "<svg") {
		return nil, errors.New("custom avatar must be an SVG document")
	}
	if err := s.Store.UpdateAgentAvatar(ctx, a.ID, strings.TrimSpace(avatar), svg); err != nil {
		return nil, err
	}
	a.Avatar, a.AvatarSVG = strings.TrimSpace(avatar), svg
	s.event(ctx, model.EventAgentIdentity, actor, a.ID, "avatar updated")
	return a, nil
}

// EgressStatus reports the configured profile and whether it is enforced.
func (s *Service) EgressStatus(ctx context.Context, idOrName string) (*EgressStatus, error) {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	st := &EgressStatus{Agent: a.Name, Profile: a.NetworkProfile}
	switch a.NetworkProfile {
	case model.NetProfileOffline:
		st.Enforced, st.Network = true, "none"
	case model.NetProfileBrokerOnly:
		st.Enforced, st.Network = true, dockerclient.NetworkBrokerOnly
		gw, err := s.Docker.NetworkGateway(ctx, dockerclient.NetworkBrokerOnly)
		if err != nil {
			st.Warning = "broker-only network not created yet; start the agent once"
		} else {
			st.Gateway = gw
			st.BrokerBaseURL = fmt.Sprintf("http://%s:%d", gw, BrokerProfilePort)
			st.Warning = "external egress is blocked; the broker must bind the gateway address. " +
				"Host services reachable on that gateway are not filtered yet (nftables task open)."
		}
	case model.NetProfileRestricted:
		st.Warning = "restricted-development is not implemented; start is refused (fail closed)"
	default:
		st.Enforced = false
		st.Network = a.NetworkMode
		st.Warning = "unrestricted legacy networking: agents can bypass the broker"
	}
	return st, nil
}

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
