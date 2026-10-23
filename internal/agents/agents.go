// Package agents implements the agent lifecycle on top of the registry,
// the mount manager and the Docker client. It is the service layer shared
// by the API (and therefore CLI and TUI).
package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

// Service manages agent identities and their containers.
type Service struct {
	Store  *persistence.Store
	Mounts *mounts.Manager
	Docker *dockerclient.Client
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
func (s *Service) Remove(ctx context.Context, idOrName, actor string, prune bool) error {
	a, err := s.Store.GetAgent(ctx, idOrName)
	if err != nil {
		return err
	}
	if cid, err := s.Docker.FindByAgent(ctx, a.ID); err == nil && cid != "" {
		_ = s.Docker.Remove(ctx, cid, true)
	}
	if err := s.Store.DeleteAgent(ctx, a.ID); err != nil {
		return err
	}
	if prune {
		if mounts, err := s.Mounts.List(a.ID); err == nil {
			for i := len(mounts) - 1; i >= 0; i-- {
				_ = s.Mounts.Remove(a.ID, baseName(mounts[i].TargetPath), true)
			}
		}
		agentRoot := filepath.Dir(s.Mounts.WorkspaceTree(a.ID)) // <root>/agents/<id>
		if filepath.Base(agentRoot) == a.ID {
			_ = os.RemoveAll(agentRoot)
		}
	}
	s.event(ctx, model.EventAgentRemoved, actor, a.ID, fmt.Sprintf("agent %q removed (prune=%v)", a.Name, prune))
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
