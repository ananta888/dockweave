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

// Unregister removes a workspace. Grants that still claim the pair (active
// or in flight) must be revoked first.
func (s *Service) Unregister(ctx context.Context, idOrName, actor string) error {
	w, err := s.Store.GetWorkspace(ctx, idOrName)
	if err != nil {
		return err
	}
	grants, err := s.Store.ListGrants(ctx, w.ID, "", false)
	if err != nil {
		return err
	}
	now := time.Now()
	var pending int
	for _, g := range grants {
		if g.Pending(now) {
			pending++
		}
	}
	if pending > 0 {
		return fmt.Errorf("%w: workspace %q still has %d pending grant(s)", ErrConflict, w.Name, pending)
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

// Grant creates a grant and applies the mount. The record is created in
// status 'mounting' and only becomes 'active' after the kernel mount has been
// applied — a failed mount leaves a visible 'mount_failed' record (cleaned up
// by ReconcileGrants) and never an active permission. Idempotent: an
// identical active grant returns the existing record.
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
		if !g.Pending(now) {
			continue
		}
		if g.AgentID == a.ID {
			if g.Status == model.GrantActive && g.Mode == mode && g.Exclusive == exclusive {
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
	// Clear crash leftovers ('mounting') so a retry is never blocked by the
	// uniqueness index.
	if err := s.Store.FailMountingGrants(ctx, w.ID, a.ID); err != nil {
		return nil, err
	}
	g := &model.Grant{
		ID: model.NewID("gr"), WorkspaceID: w.ID, AgentID: a.ID,
		Mode: mode, Exclusive: exclusive, Status: model.GrantMounting,
		CreatedAt: now, CreatedBy: actor,
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
		_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantMountFailed)
		s.event(ctx, model.EventMountFailed, actor, a.ID, w.ID, err.Error())
		return nil, fmt.Errorf("apply mount: %w (grant %s left in status mount_failed, no permission granted)", err, g.ID)
	}
	ms.TargetPath = target
	ms.Status = model.MountActive
	if err := s.Store.UpsertMount(ctx, ms); err != nil {
		return nil, s.failGrant(ctx, g, a.ID, w, actor, fmt.Errorf("record mount: %w", err))
	}
	if err := s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantActive); err != nil {
		return nil, s.failGrant(ctx, g, a.ID, w, actor, fmt.Errorf("activate grant: %w", err))
	}
	g.Status = model.GrantActive
	s.event(ctx, model.EventGrantCreated, actor, a.ID, w.ID,
		fmt.Sprintf("grant %s on %q (%s)", a.Name, w.Name, mode))
	return g, nil
}

// failGrant rolls back a half-created grant: the kernel mount is removed
// (without MNT_DETACH) and the grant marked 'mount_failed' — or
// 'unmount_failed' if the rollback unmount itself fails, leaving a visible,
// retryable state.
func (s *Service) failGrant(ctx context.Context, g *model.Grant, agentID string, w *model.Workspace, actor string, cause error) error {
	if rerr := s.Mounts.Remove(agentID, w.Name, false); rerr != nil {
		_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantUnmountFailed)
		s.event(ctx, model.EventUnmountFailed, actor, agentID, w.ID, rerr.Error())
		return fmt.Errorf("%v; rollback unmount failed (grant %s in unmount_failed, retry revoke): %w", cause, g.ID, rerr)
	}
	_ = s.Store.DeleteMount(ctx, agentID, filepath.Join(s.Mounts.WorkspaceTree(agentID), w.Name))
	_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantMountFailed)
	s.event(ctx, model.EventMountFailed, actor, agentID, w.ID, cause.Error())
	return fmt.Errorf("%v (mount rolled back, grant %s in mount_failed)", cause, g.ID)
}

// Revoke revokes a grant and removes the mount. The unmount happens *before*
// the grant is revoked: on failure the grant stays pending in status
// 'unmount_failed' (visible, retryable) — never revoked-while-mounted.
// Idempotent for already revoked grants (cleanup is still attempted).
func (s *Service) Revoke(ctx context.Context, grantID, actor string, forceUnmount bool) error {
	g, err := s.Store.GetGrant(ctx, grantID)
	if err != nil {
		return err
	}
	w, werr := s.Store.GetWorkspace(ctx, g.WorkspaceID)
	targetName := g.ID // fallback
	if werr == nil {
		targetName = w.Name
	}

	if g.RevokedAt == nil {
		if err := s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantRevoking); err != nil {
			return err
		}
		s.event(ctx, model.EventGrantRevoking, actor, g.AgentID, g.WorkspaceID,
			fmt.Sprintf("revoking grant %s on %q", g.ID, targetName))
	}
	if err := s.Mounts.Remove(g.AgentID, targetName, forceUnmount); err != nil {
		if g.RevokedAt == nil {
			_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantUnmountFailed)
		}
		s.event(ctx, model.EventUnmountFailed, actor, g.AgentID, g.WorkspaceID, err.Error())
		if errors.Is(err, mounts.ErrBusy) && !forceUnmount {
			return fmt.Errorf("grant %s left in unmount_failed (mount still live), retry revoke or use force: %w", g.ID, err)
		}
		return fmt.Errorf("unmount (grant %s left in unmount_failed, retry revoke): %w", g.ID, err)
	}
	if g.RevokedAt == nil {
		if err := s.Store.RevokeGrant(ctx, grantID); err != nil {
			// Unmount succeeded but the record failed: stay 'revoking';
			// a retry completes the revoke (Remove is idempotent).
			return fmt.Errorf("unmount ok but revoke record failed (grant %s stays revoking, retry revoke): %w", g.ID, err)
		}
	}
	_ = s.Store.DeleteMount(ctx, g.AgentID, filepath.Join(s.Mounts.WorkspaceTree(g.AgentID), targetName))
	s.event(ctx, model.EventGrantRevoked, actor, g.AgentID, g.WorkspaceID,
		fmt.Sprintf("grant %s on %q revoked", g.ID, targetName))
	return nil
}

// ReconcileGrants converges interrupted grant lifecycles after a crash or
// restart: 'mounting' creations are finalized to 'mount_failed', failed
// creations are cleaned up, and 'revoking'/'unmount_failed' revokes are
// retried until the unmount succeeds. Never uses MNT_DETACH.
func (s *Service) ReconcileGrants(ctx context.Context) {
	grants, err := s.Store.ListGrants(ctx, "", "", true)
	if err != nil {
		s.event(ctx, model.EventRecovery, "daemon", "", "",
			fmt.Sprintf("grant reconcile: list failed: %v", err))
		return
	}
	for _, g := range grants {
		switch g.Status {
		case model.GrantMounting:
			_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantMountFailed)
			s.event(ctx, model.EventRecovery, "daemon", g.AgentID, g.WorkspaceID,
				fmt.Sprintf("grant %s interrupted during creation -> mount_failed", g.ID))
			s.cleanupFailedGrant(ctx, g)
		case model.GrantMountFailed:
			s.cleanupFailedGrant(ctx, g)
		case model.GrantRevoking, model.GrantUnmountFailed:
			s.finishRevoke(ctx, g)
		}
	}
}

// cleanupFailedGrant removes leftovers of a failed grant creation: any mount
// at the grant's target and its mount record. On EBUSY the record is kept
// for visibility and the cleanup retried on the next recovery.
func (s *Service) cleanupFailedGrant(ctx context.Context, g *model.Grant) {
	targetName := g.ID
	if w, err := s.Store.GetWorkspace(ctx, g.WorkspaceID); err == nil {
		targetName = w.Name
	}
	if err := s.Mounts.Remove(g.AgentID, targetName, false); err != nil {
		s.event(ctx, model.EventUnmountFailed, "daemon", g.AgentID, g.WorkspaceID,
			fmt.Sprintf("cleanup of failed grant %s: %v (mount record kept)", g.ID, err))
		return
	}
	_ = s.Store.DeleteMount(ctx, g.AgentID, filepath.Join(s.Mounts.WorkspaceTree(g.AgentID), targetName))
}

// finishRevoke retries the unmount of an interrupted revoke and finalizes
// the grant record once the mount is gone.
func (s *Service) finishRevoke(ctx context.Context, g *model.Grant) {
	targetName := g.ID
	if w, err := s.Store.GetWorkspace(ctx, g.WorkspaceID); err == nil {
		targetName = w.Name
	}
	if err := s.Mounts.Remove(g.AgentID, targetName, false); err != nil {
		_ = s.Store.UpdateGrantStatus(ctx, g.ID, model.GrantUnmountFailed)
		s.event(ctx, model.EventUnmountFailed, "daemon", g.AgentID, g.WorkspaceID,
			fmt.Sprintf("recovery retry for grant %s failed: %v", g.ID, err))
		return
	}
	if g.RevokedAt == nil {
		if err := s.Store.RevokeGrant(ctx, g.ID); err != nil {
			return // stays 'revoking'; next recovery retries
		}
	}
	_ = s.Store.DeleteMount(ctx, g.AgentID, filepath.Join(s.Mounts.WorkspaceTree(g.AgentID), targetName))
	s.event(ctx, model.EventGrantRevoked, "daemon", g.AgentID, g.WorkspaceID,
		fmt.Sprintf("grant %s revoked during recovery", g.ID))
}

// ListGrants returns grants (filters optional).
func (s *Service) ListGrants(ctx context.Context, ws, agent string, includeRevoked bool) ([]*model.Grant, error) {
	return s.Store.ListGrants(ctx, ws, agent, includeRevoked)
}

// ListMounts returns the recorded mount states.
func (s *Service) ListMounts(ctx context.Context, agentID string) ([]*model.MountState, error) {
	return s.Store.ListMounts(ctx, agentID)
}
