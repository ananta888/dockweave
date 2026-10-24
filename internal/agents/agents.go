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
}

// ErrMemoryUnavailable is returned when memory snapshotting is not configured.
var ErrMemoryUnavailable = errors.New("memory snapshotting not configured on daemon")

// SnapshotMemory creates a memory snapshot for an agent and applies the
// retention policy afterwards.
func (s *Service) SnapshotMemory(ctx context.Context, idOrName, actor string) (*memory.Snapshot, error) {
	if s.Memory == nil {
		return nil, ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	snap, err := s.Memory.Create(a.ID)
	if err != nil {
		return nil, err
	}
	// Default retention: keep the 10 newest snapshots.
	if _, err := s.Memory.ApplyRetention(a.ID, memory.RetentionPolicy{MaxCount: 10}); err != nil {
		// Retention failure does not fail the snapshot; report via event.
		s.event(ctx, "memory.retention_failed", actor, a.ID, "retention after snapshot failed: "+err.Error())
	}
	s.event(ctx, "memory.snapshot", actor, a.ID, "snapshot "+snap.Name)
	return snap, nil
}

// ListMemorySnapshots lists snapshots of an agent.
func (s *Service) ListMemorySnapshots(ctx context.Context, idOrName string) ([]memory.Snapshot, error) {
	if s.Memory == nil {
		return nil, ErrMemoryUnavailable
	}
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	return s.Memory.List(a.ID)
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
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		return err
	}
	if cid != "" {
		if st, err := s.Docker.State(ctx, cid); err == nil && st == "running" {
			return memory.ErrBusy
		}
	}
	if err := s.Memory.Restore(a.ID, name); err != nil {
		return err
	}
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
	if err := s.Memory.Delete(a.ID, name); err != nil {
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
	if err := s.Store.CreateAgent(ctx, a); err != nil {
		return nil, err
	}
	if err := s.Mounts.PrepareAgentTree(a.ID); err != nil {
		// Roll back identity on tree failure to avoid half-created agents.
		_ = s.Store.DeleteAgent(ctx, a.ID)
		return nil, fmt.Errorf("prepare agent tree: %w", err)
	}
	s.event(ctx, model.EventAgentCreated, actor, a.ID, fmt.Sprintf("agent %q created (image %s)", a.Name, a.Image))
	return a, nil
}

// Get resolves an agent by ID or name.
func (s *Service) Get(ctx context.Context, idOrName string) (*model.Agent, error) {
	return s.Store.GetAgent(ctx, idOrName)
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
	ncid, err := s.Docker.Create(ctx, dockerclient.ContainerSpec{
		Agent:        &runtimeAgent,
		AgentDir:     s.Mounts.AgentDir(a.ID),
		MemoryDir:    s.Mounts.MemoryDir(a.ID),
		WorkspaceDir: s.Mounts.WorkspaceTree(a.ID),
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

// Remove deletes the agent identity and container. Host data (agent install,
// memory) is preserved unless prune is true.
//
// Prune safety (review-003): every cleanup error is collected and returned,
// MNT_DETACH is only used when force is explicitly set (graceful umount is
// tried first; every detached mount is audited), and the agent record is
// deleted only after cleanup succeeded — a failed prune is retryable.
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
	if !prune {
		if err := s.Store.DeleteAgent(ctx, a.ID); err != nil {
			return err
		}
		s.event(ctx, model.EventAgentRemoved, actor, a.ID, fmt.Sprintf("agent %q removed (prune=false)", a.Name))
		return nil
	}

	var errs []error
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
	if len(errs) > 0 {
		// Keep the agent record: the prune stays retryable until clean.
		return fmt.Errorf("prune incomplete (agent record kept, retry remove): %w", errors.Join(errs...))
	}
	if err := s.Store.DeleteAgent(ctx, a.ID); err != nil {
		return err
	}
	s.event(ctx, model.EventAgentRemoved, actor, a.ID,
		fmt.Sprintf("agent %q removed (prune=true, force=%v)", a.Name, force))
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
