// Package workspaces implements the workspace registry and grant logic.
// A grant expresses *intent*; the corresponding kernel mount is applied by
// the mount manager and its observed state recorded in the registry.
package workspaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

// ErrConflict reports conflicting grants (duplicate or exclusivity violation).
var ErrConflict = errors.New("grant conflict")

// MountExecutor abstracts the privileged mount operations the workspace
// service needs (enables unit tests without CAP_SYS_ADMIN).
type MountExecutor interface {
	Add(agentID, source, targetName string, mode model.AccessMode) (string, error)
	Remove(agentID, targetName string, force bool) error
	WorkspaceTree(agentID string) string
	AddAllowedRoot(path string)
}

// Service manages workspaces and grants.
type Service struct {
	Store  *persistence.Store
	Mounts MountExecutor
}

func (s *Service) event(ctx context.Context, t model.EventType, actor, agentID, wsID, msg string) {
	_ = s.Store.AppendEvent(ctx, &model.Event{
		ID: model.NewID("ev"), Type: t, Actor: actor, AgentID: agentID,
		WorkspaceID: wsID, Message: msg, Timestamp: time.Now(),
	})
}

// Register canonicalizes and registers a shareable host directory.
// Registered workspaces act as the allowlist for mount sources.
func (s *Service) Register(ctx context.Context, name, hostPath, actor string) (*model.Workspace, error) {
	if err := model.ValidateName("workspace", name); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("canonicalize path: %w", err)
	}
	fi, err := os.Stat(canon)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", canon)
	}
	w := &model.Workspace{
		ID: model.NewID("ws"), Name: name, HostPath: canon, CreatedAt: time.Now(),
	}
	if err := w.Validate(); err != nil {
		return nil, err
	}
	if err := s.Store.CreateWorkspace(ctx, w); err != nil {
		return nil, err
	}
	// Make the new workspace an allowed mount source immediately.
	s.Mounts.AddAllowedRoot(canon)
	s.event(ctx, model.EventWorkspaceAdded, actor, "", w.ID, fmt.Sprintf("workspace %q -> %s", name, canon))
	return w, nil
}

// Unregister removes a workspace. Active grants must be revoked first.
func (s *Service) Unregister(ctx context.Context, idOrName, actor string) error {
	w, err := s.Store.GetWorkspace(ctx, idOrName)
	if err != nil {
		return err
	}
	grants, err := s.Store.ListGrants(ctx, w.ID, "", false)
	if err != nil {
		return err
	}
	if len(grants) > 0 {
		return fmt.Errorf("%w: workspace %q still has %d active grant(s)", ErrConflict, w.Name, len(grants))
	}
	// Remove historical (revoked/expired) grants so the FK constraint on
	// workspaces.id permits deletion.
	if err := s.Store.PurgeGrants(ctx, w.ID); err != nil {
		return fmt.Errorf("purge grants: %w", err)
	}
	if err := s.Store.DeleteWorkspace(ctx, w.ID); err != nil {
		return err
	}
	s.event(ctx, model.EventWorkspaceGone, actor, "", w.ID, fmt.Sprintf("workspace %q unregistered", w.Name))
	return nil
}

// Get resolves a workspace by ID or name.
func (s *Service) Get(ctx context.Context, idOrName string) (*model.Workspace, error) {
	return s.Store.GetWorkspace(ctx, idOrName)
}

// List returns all workspaces.
func (s *Service) List(ctx context.Context) ([]*model.Workspace, error) {
	return s.Store.ListWorkspaces(ctx)
}

// Grant creates a grant and applies the mount. Idempotent: an identical
// active grant returns the existing record.
func (s *Service) Grant(ctx context.Context, wsName, agentID string, mode model.AccessMode, exclusive bool, ttl time.Duration, actor string) (*model.Grant, error) {
	if mode != model.ModeRO && mode != model.ModeRW {
		return nil, fmt.Errorf("mode must be ro or rw, got %q", mode)
	}
	w, err := s.Store.GetWorkspace(ctx, wsName)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	a, err := s.Store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	active, err := s.Store.ListGrants(ctx, w.ID, "", false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, g := range active {
		if !g.Active(now) {
			continue
		}
		if g.AgentID == a.ID {
			if g.Mode == mode && g.Exclusive == exclusive {
				return g, nil // idempotent
			}
			return nil, fmt.Errorf("%w: agent already holds a %s grant on %q (revoke first)",
				ErrConflict, g.Mode, w.Name)
		}
		if exclusive && mode == model.ModeRW && g.Mode == model.ModeRW {
			return nil, fmt.Errorf("%w: workspace %q already has an active rw grant (agent %s); exclusive requested",
				ErrConflict, w.Name, g.AgentID)
		}
		if g.Exclusive && g.Mode == model.ModeRW && mode == model.ModeRW {
			return nil, fmt.Errorf("%w: workspace %q is exclusively granted to agent %s",
				ErrConflict, w.Name, g.AgentID)
		}
	}
	g := &model.Grant{
		ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID,
		Mode: mode, Exclusive: exclusive, CreatedAt: now, CreatedBy: actor,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		g.ExpiresAt = &exp
	}
	if err := s.Store.CreateGrant(ctx, g); err != nil {
		return nil, err
	}
	// Apply the kernel mount (workspace name is the target under /workspace).
	target, err := s.Mounts.Add(a.ID, w.HostPath, w.Name, mode)
	ms := &model.MountState{
		ID: model.NewID("mnt"), AgentID: a.ID, GrantID: g.ID,
		HostPath: w.HostPath, Mode: mode, UpdatedAt: now,
	}
	if err != nil {
		ms.Status = model.MountFailed
		ms.LastError = err.Error()
		ms.TargetPath = filepath.Join(s.Mounts.WorkspaceTree(a.ID), w.Name)
		_ = s.Store.UpsertMount(ctx, ms)
		s.event(ctx, model.EventMountFailed, actor, a.ID, w.ID, err.Error())
		return nil, fmt.Errorf("apply mount: %w", err)
	}
	ms.TargetPath = target
	ms.Status = model.MountActive
	if err := s.Store.UpsertMount(ctx, ms); err != nil {
		return nil, err
	}
	s.event(ctx, model.EventGrantCreated, actor, a.ID, w.ID,
		fmt.Sprintf("grant %s on %q (%s)", a.Name, w.Name, mode))
	return g, nil
}

// Revoke revokes a grant and removes the mount. Idempotent for already
// revoked grants.
func (s *Service) Revoke(ctx context.Context, grantID, actor string, forceUnmount bool) error {
	g, err := s.Store.GetGrant(ctx, grantID)
	if err != nil {
		return err
	}
	w, werr := s.Store.GetWorkspace(ctx, g.WorkspaceID)
	if g.RevokedAt == nil {
		if err := s.Store.RevokeGrant(ctx, grantID); err != nil {
			return err
		}
	}
	targetName := g.ID // fallback
	if werr == nil {
		targetName = w.Name
	}
	if err := s.Mounts.Remove(g.AgentID, targetName, forceUnmount); err != nil {
		if errors.Is(err, mounts.ErrBusy) && !forceUnmount {
			return fmt.Errorf("grant revoked, but unmount failed: %w (retry with force or stop the agent)", err)
		}
		return fmt.Errorf("unmount: %w", err)
	}
	_ = s.Store.DeleteMount(ctx, g.AgentID, filepath.Join(s.Mounts.WorkspaceTree(g.AgentID), targetName))
	s.event(ctx, model.EventGrantRevoked, actor, g.AgentID, g.WorkspaceID, "")
	return nil
}

// ListGrants returns grants (filters optional).
func (s *Service) ListGrants(ctx context.Context, ws, agent string, includeRevoked bool) ([]*model.Grant, error) {
	return s.Store.ListGrants(ctx, ws, agent, includeRevoked)
}

// ListMounts returns the recorded mount states.
func (s *Service) ListMounts(ctx context.Context, agentID string) ([]*model.MountState, error) {
	return s.Store.ListMounts(ctx, agentID)
}
