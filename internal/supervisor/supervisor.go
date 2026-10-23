// Package supervisor performs startup recovery and periodic
// desired-vs-actual reconciliation.
package supervisor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mounts"
	"github.com/ananta888/dockweave/internal/persistence"
)

// Supervisor reconciles registry state with kernel and engine reality.
type Supervisor struct {
	Store  *persistence.Store
	Mounts *mounts.Manager
	Docker *dockerclient.Client
}

// Recover runs once at daemon start:
//  1. Container states are refreshed from the engine (DB may be stale).
//  2. Agent trees are re-prepared (rshared) if missing.
//  3. Desired mounts are re-applied; foreign mounts are reported, never removed.
func (s *Supervisor) Recover(ctx context.Context) error {
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("list agents for recovery: %w", err)
	}
	for _, a := range agents {
		s.recoverAgent(ctx, a)
	}
	return nil
}

func (s *Supervisor) recoverAgent(ctx context.Context, a *model.Agent) {
	// 1. Refresh container state.
	cid, err := s.Docker.FindByAgent(ctx, a.ID)
	if err != nil {
		log.Printf("recovery: docker lookup for agent %s: %v", a.Name, err)
	} else if cid != "" {
		st, err := s.Docker.State(ctx, cid)
		if err == nil {
			mapped := model.AgentState(st)
			switch st {
			case "running":
				mapped = model.AgentRunning
			case "exited", "dead":
				mapped = model.AgentExited
			case "created":
				mapped = model.AgentCreated
			case "paused":
				mapped = model.AgentStopped
			}
			if mapped != a.State || cid != a.ContainerID {
				_ = s.Store.UpdateAgentState(ctx, a.ID, mapped, cid)
			}
		}
	} else if a.State == model.AgentRunning {
		_ = s.Store.UpdateAgentState(ctx, a.ID, model.AgentExited, "")
	}

	// 2. Re-prepare the workspace tree (idempotent).
	if err := s.Mounts.PrepareAgentTree(a.ID); err != nil {
		log.Printf("recovery: prepare tree for agent %s: %v", a.Name, err)
		_ = s.Store.AppendEvent(ctx, &model.Event{
			ID: model.NewID("ev"), Type: model.EventRecovery, Actor: "daemon",
			AgentID: a.ID, Message: "prepare tree failed: " + err.Error(), Timestamp: time.Now(),
		})
		return
	}

	// 3. Reconcile desired mounts.
	desired, err := s.Store.ListMounts(ctx, a.ID)
	if err != nil {
		log.Printf("recovery: list mounts for agent %s: %v", a.Name, err)
		return
	}
	var want []model.MountState
	for _, d := range desired {
		if d.Status != model.MountGone {
			want = append(want, *d)
		}
	}
	res, err := s.Mounts.Reconcile(a.ID, want)
	if err != nil {
		log.Printf("recovery: reconcile agent %s: %v", a.Name, err)
		return
	}
	if len(res.Restored) > 0 || len(res.Orphaned) > 0 || len(res.Failed) > 0 {
		msg := fmt.Sprintf("restored=%v orphaned=%v failed=%v", res.Restored, res.Orphaned, res.Failed)
		log.Printf("recovery: agent %s: %s", a.Name, msg)
		_ = s.Store.AppendEvent(ctx, &model.Event{
			ID: model.NewID("ev"), Type: model.EventRecovery, Actor: "daemon",
			AgentID: a.ID, Message: msg, Timestamp: time.Now(),
		})
	}
	// Update recorded status for mounts we verified.
	actual, err := s.Mounts.List(a.ID)
	if err != nil {
		return
	}
	actualSet := map[string]bool{}
	for _, m := range actual {
		actualSet[m.TargetPath] = true
	}
	for _, d := range desired {
		status := model.MountGone
		if actualSet[d.TargetPath] {
			status = model.MountActive
		} else if d.Status == model.MountFailed {
			status = model.MountFailed
		} else if d.Status != model.MountGone {
			status = model.MountDesired
		}
		if status != d.Status {
			d.Status = status
			d.UpdatedAt = time.Now()
			_ = s.Store.UpsertMount(ctx, d)
		}
	}
}
