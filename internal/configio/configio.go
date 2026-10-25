// Package configio exports and imports the complete DockWeave operator
// configuration as one JSON bundle: everything needed to recreate the
// agent setup on another machine (agent identities, workspaces, grants,
// memory bindings and paths) — and nothing else: no audit logs, no
// runtime mount state, no container state, no snapshots (memory data is
// carried with the memory paths / snapshots separately).
package configio

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananta888/dockweave/internal/agents"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/workspaces"
)

// Format identifies the bundle layout.
const Format = "dockweave-config-v1"

// Bundle is the portable operator configuration.
type Bundle struct {
	Format       string            `json:"format"`
	ExportedAt   time.Time         `json:"exported_at"`
	DaemonConfig map[string]string `json:"daemon_config,omitempty"` // informational for the target host
	Agents       []AgentSpec       `json:"agents"`
	Workspaces   []WorkspaceSpec   `json:"workspaces"`
	Grants       []GrantSpec       `json:"grants"`
	Memories     []MemorySpec      `json:"memories"`
}

// AgentSpec is a portable agent identity (no runtime state).
type AgentSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Image       string   `json:"image"`
	Command     []string `json:"command,omitempty"`
	Env         []string `json:"env,omitempty"`
	EnvFile     string   `json:"env_file,omitempty"`
	AgentRO     bool     `json:"agent_ro"`
	UID         int      `json:"uid,omitempty"`
	GID         int      `json:"gid,omitempty"`
	CPULimit    float64  `json:"cpu_limit,omitempty"`
	MemoryMB    int64    `json:"memory_mb,omitempty"`
	NetworkMode string   `json:"network_mode,omitempty"`
	AgentDir    string   `json:"agent_dir,omitempty"`
	NotesDir    string   `json:"notes_dir,omitempty"`
}

// WorkspaceSpec is a registered shareable host directory.
type WorkspaceSpec struct {
	Name     string `json:"name"`
	HostPath string `json:"host_path"`
}

// GrantSpec binds a workspace to an agent (references by name).
type GrantSpec struct {
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Mode      string `json:"mode"`
	Exclusive bool   `json:"exclusive,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339; import keeps the remaining time
}

// MemorySpec is a memory identity and its store location. The store
// *contents* travel with the path (or via memory snapshots).
type MemorySpec struct {
	Owner string `json:"owner,omitempty"` // agent name; empty = unbound
	Path  string `json:"path,omitempty"`  // custom store dir; empty = default layout
}

// ImportResult reports what the import did, per entity.
type ImportResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
	Errors  []string `json:"errors"`
	// ConfigHint lists the daemon settings the bundle recorded, for the
	// operator to apply on the target host (/etc/dockweave/dockweaved.conf).
	ConfigHint map[string]string `json:"config_hint,omitempty"`
}

// Service orchestrates export/import across the domain services.
type Service struct {
	Store        *persistence.Store
	Agents       *agents.Service
	Workspaces   *workspaces.Service
	DaemonConfig map[string]string // recorded into exports (informational)
}

// Export collects the complete operator configuration as JSON data.
func (s *Service) Export(ctx context.Context) (*Bundle, error) {
	b := &Bundle{
		Format:       Format,
		ExportedAt:   time.Now().UTC(),
		DaemonConfig: s.DaemonConfig,
	}
	agts, err := s.Store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{} // id -> name
	for _, a := range agts {
		names[a.ID] = a.Name
		b.Agents = append(b.Agents, AgentSpec{
			Name: a.Name, Type: a.Type, Image: a.Image,
			Command: a.Command, Env: a.Env, EnvFile: a.EnvFile,
			AgentRO: a.AgentRO, UID: a.UID, GID: a.GID,
			CPULimit: a.CPULimit, MemoryMB: a.MemoryMB,
			NetworkMode: a.NetworkMode, AgentDir: a.AgentDir, NotesDir: a.NotesDir,
		})
	}
	ws, err := s.Store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	wsNames := map[string]string{}
	for _, w := range ws {
		wsNames[w.ID] = w.Name
		b.Workspaces = append(b.Workspaces, WorkspaceSpec{Name: w.Name, HostPath: w.HostPath})
	}
	grants, err := s.Store.ListGrants(ctx, "", "", true)
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		if g.RevokedAt != nil {
			continue // historical grants are not part of a setup
		}
		gs := GrantSpec{
			Workspace: wsNames[g.WorkspaceID], Agent: names[g.AgentID],
			Mode: string(g.Mode), Exclusive: g.Exclusive,
		}
		if g.ExpiresAt != nil {
			gs.ExpiresAt = g.ExpiresAt.UTC().Format(time.RFC3339)
		}
		b.Grants = append(b.Grants, gs)
	}
	mems, err := s.Store.ListMemories(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range mems {
		ms := MemorySpec{Owner: names[m.OwnerAgentID], Path: m.Path}
		if ms.Path == "" && s.Agents.Memory != nil {
			ms.Path = s.Agents.Memory.Path(m.ID) // resolved default store
		}
		b.Memories = append(b.Memories, ms)
	}
	return b, nil
}

// Import recreates a configuration on this deployment. Missing entities
// are created; existing ones are skipped (idempotent). References use
// names; memory stores keep their host paths so the data directories can
// simply be copied.
func (s *Service) Import(ctx context.Context, b *Bundle) (*ImportResult, error) {
	if b.Format != Format {
		return nil, fmt.Errorf("unsupported bundle format %q (want %q)", b.Format, Format)
	}
	res := &ImportResult{ConfigHint: b.DaemonConfig}

	// Memories first: agents attach to them later. Existing stores (same
	// path) are reused so imports stay idempotent.
	memByName := map[string]string{} // owner name -> memory id
	existing, _ := s.Store.ListMemories(ctx)
	memByPath := map[string]string{}
	for _, m := range existing {
		if m.Path != "" {
			memByPath[m.Path] = m.ID
		}
	}
	for _, ms := range b.Memories {
		if id, ok := memByPath[ms.Path]; ok {
			if ms.Owner != "" {
				memByName[ms.Owner] = id
			}
			res.Skipped = append(res.Skipped, "memory "+ms.Path+" (already exists)")
			continue
		}
		mem := &model.Memory{ID: model.NewID("mem"), Path: ms.Path, CreatedAt: time.Now()}
		if err := s.Store.CreateMemory(ctx, mem); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("memory %s: %v", ms.Path, err))
			continue
		}
		if s.Agents.Memory != nil {
			s.Agents.Memory.SetPath(mem.ID, ms.Path)
		}
		if ms.Owner != "" {
			memByName[ms.Owner] = mem.ID
		}
		res.Created = append(res.Created, "memory "+ms.Path)
	}

	agentID := map[string]string{}
	for _, as := range b.Agents {
		a := &model.Agent{
			Name: as.Name, Type: as.Type, Image: as.Image,
			Command: as.Command, Env: as.Env, EnvFile: as.EnvFile,
			AgentRO: as.AgentRO, UID: as.UID, GID: as.GID,
			CPULimit: as.CPULimit, MemoryMB: as.MemoryMB,
			NetworkMode: as.NetworkMode, AgentDir: as.AgentDir, NotesDir: as.NotesDir,
		}
		created, err := s.Agents.Create(ctx, a, "config-import")
		if err != nil {
			if isExists(err) {
				if old, oerr := s.Store.GetAgent(ctx, as.Name); oerr == nil {
					agentID[old.Name] = old.ID
				}
				res.Skipped = append(res.Skipped, "agent "+as.Name+" (already exists)")
				continue
			}
			res.Errors = append(res.Errors, fmt.Sprintf("agent %s: %v", as.Name, err))
			continue
		}
		agentID[created.Name] = created.ID
		res.Created = append(res.Created, "agent "+as.Name)
		// custom folder assignments (paths may not exist yet on a fresh
		// host — recorded verbatim, applied at the next agent start).
		if as.AgentDir != "" {
			if _, err := s.Agents.SetAgentDir(ctx, created.Name, as.AgentDir, "config-import"); err != nil {
				_ = s.Store.UpdateAgentDir(ctx, created.ID, as.AgentDir)
			}
		}
		if as.NotesDir != "" {
			if _, err := s.Agents.SetNotesDir(ctx, created.Name, as.NotesDir, "config-import"); err != nil {
				_ = s.Store.UpdateNotesDir(ctx, created.ID, as.NotesDir)
			}
		}
		// Re-bind the exported memory store to the recreated agent; the
		// auto-created store from Create is replaced (it is empty).
		if memID, ok := memByName[as.Name]; ok {
			if cur, err := s.Store.GetMemoryByAgent(ctx, created.ID); err == nil && cur.ID != memID {
				_ = s.Store.SetMemoryOwner(ctx, cur.ID, "")
				_ = s.Store.DeleteMemory(ctx, cur.ID)
			}
			if err := s.Store.SetMemoryOwner(ctx, memID, created.ID); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("bind memory of %s: %v", as.Name, err))
			}
		}
	}

	wsID := map[string]string{}
	for _, ws := range b.Workspaces {
		w, err := s.Workspaces.Register(ctx, ws.Name, ws.HostPath, "config-import")
		if err != nil {
			if isExists(err) {
				if old, oerr := s.Store.GetWorkspace(ctx, ws.Name); oerr == nil {
					wsID[old.Name] = old.ID
				}
				res.Skipped = append(res.Skipped, "workspace "+ws.Name+" (already exists)")
				continue
			}
			res.Errors = append(res.Errors, fmt.Sprintf("workspace %s: %v", ws.Name, err))
			continue
		}
		wsID[w.Name] = w.ID
		res.Created = append(res.Created, "workspace "+ws.Name)
	}

	// Grants: skip equivalents that already exist (idempotent import).
	current, _ := s.Store.ListGrants(ctx, "", "", false)
	for _, gs := range b.Grants {
		dup := false
		for _, g := range current {
			if g.AgentID == agentID[gs.Agent] && g.WorkspaceID == wsID[gs.Workspace] &&
				string(g.Mode) == gs.Mode && g.Active(time.Now()) {
				dup = true
				break
			}
		}
		if dup {
			res.Skipped = append(res.Skipped, fmt.Sprintf("grant %s/%s (already exists)", gs.Workspace, gs.Agent))
			continue
		}
		var ttlDur time.Duration
		if gs.ExpiresAt != "" {
			if exp, err := time.Parse(time.RFC3339, gs.ExpiresAt); err == nil {
				if d := time.Until(exp); d > 0 {
					ttlDur = d
				} else {
					res.Skipped = append(res.Skipped, fmt.Sprintf("grant %s/%s (expired)", gs.Workspace, gs.Agent))
					continue
				}
			}
		}
		if _, err := s.Workspaces.Grant(ctx, gs.Workspace, gs.Agent, model.AccessMode(gs.Mode), gs.Exclusive, ttlDur, "config-import"); err != nil {
			if isExists(err) {
				res.Skipped = append(res.Skipped, fmt.Sprintf("grant %s/%s (already exists)", gs.Workspace, gs.Agent))
				continue
			}
			res.Errors = append(res.Errors, fmt.Sprintf("grant %s/%s: %v", gs.Workspace, gs.Agent, err))
			continue
		}
		res.Created = append(res.Created, fmt.Sprintf("grant %s/%s", gs.Workspace, gs.Agent))
	}
	return res, nil
}

// isExists reports whether err is a uniqueness conflict (entity already
// exists on the target deployment).
func isExists(err error) bool {
	return errors.Is(err, persistence.ErrConflict)
}
