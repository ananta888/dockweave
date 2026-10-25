// Package persistence implements the SQLite-backed registry of DockWeave.
// It is the only package that talks to the database. All mutating access
// is serialized through a single *sql.DB guarded by an explicit write mutex
// because SQLite allows exactly one writer at a time.
package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ananta888/dockweave/internal/model"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on uniqueness violations (duplicate names, double grants).
var ErrConflict = errors.New("conflict")

const schemaVersion = 5

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS agents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL,
		image TEXT NOT NULL,
		command TEXT NOT NULL DEFAULT '[]',
		env TEXT NOT NULL DEFAULT '[]',
		env_file TEXT NOT NULL DEFAULT '',
		agent_ro INTEGER NOT NULL DEFAULT 1,
		uid INTEGER NOT NULL DEFAULT 0,
		gid INTEGER NOT NULL DEFAULT 0,
		cpu_limit REAL NOT NULL DEFAULT 0,
		memory_mb INTEGER NOT NULL DEFAULT 0,
		network_mode TEXT NOT NULL DEFAULT 'bridge',
		state TEXT NOT NULL DEFAULT 'created',
		container_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		host_path TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS grants (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id),
		agent_id TEXT NOT NULL REFERENCES agents(id),
		mode TEXT NOT NULL,
		exclusive INTEGER NOT NULL DEFAULT 0,
		expires_at TEXT,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		revoked_at TEXT
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_active
		ON grants(workspace_id, agent_id) WHERE revoked_at IS NULL;
	CREATE TABLE IF NOT EXISTS mounts (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		grant_id TEXT NOT NULL DEFAULT '',
		host_path TEXT NOT NULL,
		target_path TEXT NOT NULL,
		mode TEXT NOT NULL,
		status TEXT NOT NULL,
		last_error TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL,
		UNIQUE(agent_id, target_path)
	);
	CREATE TABLE IF NOT EXISTS events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL,
		type TEXT NOT NULL,
		actor TEXT NOT NULL DEFAULT '',
		agent_id TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		ts TEXT NOT NULL,
		prev_hash TEXT NOT NULL DEFAULT '',
		hash TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	// 2: grant lifecycle status (review-002). Failed creations must not block
	// retries: the uniqueness claim covers only non-terminal, non-failed states.
	`ALTER TABLE grants ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
	UPDATE grants SET status='revoked' WHERE revoked_at IS NOT NULL;
	DROP INDEX IF EXISTS idx_grants_active;
	CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_active
		ON grants(workspace_id, agent_id)
		WHERE revoked_at IS NULL AND status IN ('mounting','active','revoking','unmount_failed');`,
	// 3: memory identities decoupled from agents (review-009). Exactly one
	// write owner per memory; unbound memories have a NULL owner.
	`CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		owner_agent_id TEXT UNIQUE,
		created_at TEXT NOT NULL,
		attached_at TEXT
	);`,
	// 4: custom folder assignments (review follow-up): /agent and /memory
	// may point at user-registered host directories; empty = default layout.
	`ALTER TABLE agents ADD COLUMN agent_dir TEXT NOT NULL DEFAULT '';
	ALTER TABLE memories ADD COLUMN path TEXT NOT NULL DEFAULT '';`,
	// 5: per-agent knowledge folder (/notes) with an optional custom path.
	`ALTER TABLE agents ADD COLUMN notes_dir TEXT NOT NULL DEFAULT '';`,
}

// Store is the DockWeave registry.
type Store struct {
	db   *sql.DB
	path string
}

// Path returns the filesystem path of the database file.
func (s *Store) Path() string { return s.path }

// Open opens (and if needed creates/migrates) the registry at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer semantics, simplifies reasoning
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	row := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`)
	var vs string
	if err := row.Scan(&vs); err == nil {
		fmt.Sscanf(vs, "%d", &v)
	}
	if v >= schemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	for i := v; i < schemaVersion; i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("%d", schemaVersion)); err != nil {
		tx.Rollback()
		return fmt.Errorf("set schema version: %w", err)
	}
	return tx.Commit()
}

const tsLayout = time.RFC3339Nano

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) (time.Time, error) {
	return time.Parse(tsLayout, s)
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc.org/sqlite reports SQLITE_CONSTRAINT_UNIQUE (2067) in the message.
	return strings.Contains(msg, "constraint failed") || strings.Contains(msg, "UNIQUE constraint")
}

// ---- Agents ----

// CreateAgent inserts a new agent identity.
func (s *Store) CreateAgent(ctx context.Context, a *model.Agent) error {
	cmd, _ := json.Marshal(a.Command)
	env, _ := json.Marshal(a.Env)
	_, err := s.db.ExecContext(ctx, `INSERT INTO agents
		(id,name,type,image,command,env,env_file,agent_ro,uid,gid,cpu_limit,memory_mb,network_mode,state,container_id,agent_dir,notes_dir,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Type, a.Image, string(cmd), string(env), a.EnvFile, a.AgentRO,
		a.UID, a.GID, a.CPULimit, a.MemoryMB, a.NetworkMode, string(a.State), a.ContainerID,
		a.AgentDir, a.NotesDir, ts(a.CreatedAt), ts(a.UpdatedAt))
	if isUniqueErr(err) {
		return fmt.Errorf("%w: agent name %q", ErrConflict, a.Name)
	}
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	return nil
}

// GetAgent fetches an agent by ID or name (name lookup if no ID match).
func (s *Store) GetAgent(ctx context.Context, idOrName string) (*model.Agent, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,type,image,command,env,env_file,agent_ro,uid,gid,
		cpu_limit,memory_mb,network_mode,state,container_id,agent_dir,notes_dir,created_at,updated_at
		FROM agents WHERE id=? OR name=?`, idOrName, idOrName)
	return scanAgent(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanAgent(row scanner) (*model.Agent, error) {
	var a model.Agent
	var cmd, env, ca, ua, state string
	var ro bool
	if err := row.Scan(&a.ID, &a.Name, &a.Type, &a.Image, &cmd, &env, &a.EnvFile, &ro,
		&a.UID, &a.GID, &a.CPULimit, &a.MemoryMB, &a.NetworkMode, &state, &a.ContainerID, &a.AgentDir, &a.NotesDir, &ca, &ua); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan agent: %w", err)
	}
	a.AgentRO = ro
	a.State = model.AgentState(state)
	json.Unmarshal([]byte(cmd), &a.Command)
	json.Unmarshal([]byte(env), &a.Env)
	a.CreatedAt, _ = parseTS(ca)
	a.UpdatedAt, _ = parseTS(ua)
	return &a, nil
}

// UpdateAgentConfig updates the mutable agent configuration (type, image,
// command, uid/gid). Applied to the container at the next start.
func (s *Store) UpdateAgentConfig(ctx context.Context, a *model.Agent) error {
	cmd, _ := json.Marshal(a.Command)
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET type=?, image=?, command=?, uid=?, gid=?, updated_at=? WHERE id=?`,
		string(a.Type), a.Image, string(cmd), a.UID, a.GID, ts(time.Now()), a.ID)
	if err != nil {
		return fmt.Errorf("update agent config: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAgentDir sets (or with an empty dir clears) the custom /agent
// source directory of an agent.
func (s *Store) UpdateAgentDir(ctx context.Context, id, dir string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET agent_dir=?, updated_at=? WHERE id=?`,
		dir, ts(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update agent dir: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateNotesDir sets (or with an empty dir clears) the custom /notes
// source directory of an agent (per-agent knowledge folder).
func (s *Store) UpdateNotesDir(ctx context.Context, id, dir string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET notes_dir=?, updated_at=? WHERE id=?`,
		dir, ts(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update notes dir: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAgents returns all agents ordered by name.
func (s *Store) ListAgents(ctx context.Context) ([]*model.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,type,image,command,env,env_file,agent_ro,uid,gid,
		cpu_limit,memory_mb,network_mode,state,container_id,agent_dir,notes_dir,created_at,updated_at FROM agents ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []*model.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAgentState persists state/container changes of an agent.
func (s *Store) UpdateAgentState(ctx context.Context, id string, state model.AgentState, containerID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET state=?, container_id=?, updated_at=? WHERE id=?`,
		string(state), containerID, ts(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update agent state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAgent removes the agent identity row.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Workspaces ----

// CreateWorkspace registers a workspace.
func (s *Store) CreateWorkspace(ctx context.Context, w *model.Workspace) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspaces(id,name,host_path,created_at) VALUES(?,?,?,?)`,
		w.ID, w.Name, w.HostPath, ts(w.CreatedAt))
	if isUniqueErr(err) {
		return fmt.Errorf("%w: workspace name or path", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert workspace: %w", err)
	}
	return nil
}

// GetWorkspace fetches a workspace by ID or name.
func (s *Store) GetWorkspace(ctx context.Context, idOrName string) (*model.Workspace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,host_path,created_at FROM workspaces WHERE id=? OR name=?`, idOrName, idOrName)
	return scanWorkspace(row)
}

func scanWorkspace(row scanner) (*model.Workspace, error) {
	var w model.Workspace
	var ca string
	if err := row.Scan(&w.ID, &w.Name, &w.HostPath, &ca); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	w.CreatedAt, _ = parseTS(ca)
	return &w, nil
}

// ListWorkspaces returns all workspaces.
func (s *Store) ListWorkspaces(ctx context.Context) ([]*model.Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,host_path,created_at FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	var out []*model.Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWorkspace removes a workspace row.
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Grants ----

// CreateGrant inserts a grant. The partial unique index prevents duplicate
// pending grants for the same (workspace, agent) pair.
func (s *Store) CreateGrant(ctx context.Context, g *model.Grant) error {
	var exp *string
	if g.ExpiresAt != nil {
		e := ts(*g.ExpiresAt)
		exp = &e
	}
	status := g.Status
	if status == "" {
		status = model.GrantActive
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO grants(id,workspace_id,agent_id,mode,exclusive,status,expires_at,created_at,created_by)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		g.ID, g.WorkspaceID, g.AgentID, string(g.Mode), g.Exclusive, string(status), exp, ts(g.CreatedAt), g.CreatedBy)
	if isUniqueErr(err) {
		return fmt.Errorf("%w: pending grant for workspace/agent pair exists", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert grant: %w", err)
	}
	return nil
}

const grantCols = `id,workspace_id,agent_id,mode,exclusive,status,expires_at,created_at,created_by,revoked_at`

func scanGrant(row scanner) (*model.Grant, error) {
	var g model.Grant
	var exp, rev, ca, status sql.NullString
	var excl bool
	var mode string
	if err := row.Scan(&g.ID, &g.WorkspaceID, &g.AgentID, &mode, &excl, &status, &exp, &ca, &g.CreatedBy, &rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan grant: %w", err)
	}
	g.Mode = model.AccessMode(mode)
	g.Exclusive = excl
	g.Status = model.GrantStatus(status.String)
	if g.Status == "" {
		g.Status = model.GrantActive
	}
	g.CreatedAt, _ = parseTS(ca.String)
	if exp.Valid {
		if t, err := parseTS(exp.String); err == nil {
			g.ExpiresAt = &t
		}
	}
	if rev.Valid {
		if t, err := parseTS(rev.String); err == nil {
			g.RevokedAt = &t
		}
	}
	return &g, nil
}

// GetGrant fetches a grant by ID.
func (s *Store) GetGrant(ctx context.Context, id string) (*model.Grant, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+grantCols+` FROM grants WHERE id=?`, id)
	return scanGrant(row)
}

// ListGrants returns grants, optionally filtered by workspace or agent.
func (s *Store) ListGrants(ctx context.Context, workspaceID, agentID string, includeRevoked bool) ([]*model.Grant, error) {
	q := `SELECT ` + grantCols + ` FROM grants WHERE 1=1`
	var args []any
	if workspaceID != "" {
		q += ` AND workspace_id=?`
		args = append(args, workspaceID)
	}
	if agentID != "" {
		q += ` AND agent_id=?`
		args = append(args, agentID)
	}
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()
	var out []*model.Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGrantStatus transitions a grant to a new lifecycle status.
func (s *Store) UpdateGrantStatus(ctx context.Context, id string, status model.GrantStatus) error {
	res, err := s.db.ExecContext(ctx, `UPDATE grants SET status=? WHERE id=?`, string(status), id)
	if err != nil {
		return fmt.Errorf("update grant status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FailMountingGrants marks all grants stuck in 'mounting' for a
// (workspace, agent) pair as 'mount_failed' so a retry is not blocked by the
// uniqueness index. Used before creating a new grant and by recovery.
func (s *Store) FailMountingGrants(ctx context.Context, workspaceID, agentID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE grants SET status='mount_failed' WHERE workspace_id=? AND agent_id=? AND status='mounting'`,
		workspaceID, agentID)
	if err != nil {
		return fmt.Errorf("fail mounting grants: %w", err)
	}
	return nil
}

// RevokeGrant marks a grant revoked.
func (s *Store) RevokeGrant(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE grants SET revoked_at=?, status='revoked' WHERE id=? AND revoked_at IS NULL`,
		ts(time.Now()), id)
	if err != nil {
		return fmt.Errorf("revoke grant: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeGrants deletes all grant rows (including revoked) for a workspace.
func (s *Store) PurgeGrants(ctx context.Context, workspaceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM grants WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return fmt.Errorf("purge grants: %w", err)
	}
	return nil
}

// PurgeGrantsByAgent deletes all grant rows (including historical) of an
// agent. Called during agent removal after revoking — the FK on
// grants.agent_id would otherwise block the delete.
func (s *Store) PurgeGrantsByAgent(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM grants WHERE agent_id=?`, agentID)
	if err != nil {
		return fmt.Errorf("purge grants by agent: %w", err)
	}
	return nil
}

// ---- Memories (decoupled identity, single write owner) ----

// CreateMemory inserts a memory identity. ownerAgentID may be empty
// (unbound).
func (s *Store) CreateMemory(ctx context.Context, m *model.Memory) error {
	var owner, attached *string
	if m.OwnerAgentID != "" {
		owner = &m.OwnerAgentID
	}
	if m.AttachedAt != nil {
		a := ts(*m.AttachedAt)
		attached = &a
	}
	var path string
	if m.Path != "" {
		path = m.Path
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories(id,owner_agent_id,path,created_at,attached_at) VALUES(?,?,?,?,?)`,
		m.ID, owner, path, ts(m.CreatedAt), attached)
	if isUniqueErr(err) {
		return fmt.Errorf("%w: agent already owns a memory", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

func scanMemory(row scanner) (*model.Memory, error) {
	var m model.Memory
	var owner, ca, aa, path sql.NullString
	if err := row.Scan(&m.ID, &owner, &path, &ca, &aa); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	m.OwnerAgentID = owner.String
	m.Path = path.String
	m.CreatedAt, _ = parseTS(ca.String)
	if aa.Valid {
		if t, err := parseTS(aa.String); err == nil {
			m.AttachedAt = &t
		}
	}
	return &m, nil
}

const memoryCols = `id,owner_agent_id,path,created_at,attached_at`

// GetMemory fetches a memory identity by ID.
func (s *Store) GetMemory(ctx context.Context, id string) (*model.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `SELECT `+memoryCols+` FROM memories WHERE id=?`, id))
}

// GetMemoryByAgent returns the memory currently owned by an agent.
func (s *Store) GetMemoryByAgent(ctx context.Context, agentID string) (*model.Memory, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `SELECT `+memoryCols+` FROM memories WHERE owner_agent_id=?`, agentID))
}

// ListMemories returns all memory identities.
func (s *Store) ListMemories(ctx context.Context) ([]*model.Memory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryCols+` FROM memories ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	var out []*model.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMemoryOwner binds a memory to an agent (or unbinds when agentID is
// empty). The single-writer rule is enforced in both directions: a memory
// cannot be taken from another owner (no steal) and an agent cannot own a
// second memory (UNIQUE owner_agent_id). Violations surface as ErrConflict.
func (s *Store) SetMemoryOwner(ctx context.Context, id, agentID string) error {
	now := ts(time.Now())
	var res sql.Result
	var err error
	if agentID == "" {
		res, err = s.db.ExecContext(ctx,
			`UPDATE memories SET owner_agent_id=NULL, attached_at=NULL WHERE id=?`, id)
	} else {
		// Bind only when unbound or already bound to the same agent: taking
		// over a memory owned by a different agent is rejected.
		res, err = s.db.ExecContext(ctx,
			`UPDATE memories SET owner_agent_id=?, attached_at=? WHERE id=? AND (owner_agent_id IS NULL OR owner_agent_id=?)`,
			agentID, now, id, agentID)
	}
	if isUniqueErr(err) {
		return fmt.Errorf("%w: memory already has a writer or agent already owns a memory", ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("set memory owner: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "no such memory" from "already owned by someone else".
		var exists string
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM memories WHERE id=?`, id).Scan(&exists); err != nil {
			return ErrNotFound
		}
		return fmt.Errorf("%w: memory %s is owned by another agent", ErrConflict, id)
	}
	return nil
}

// DeleteMemory removes a memory identity (its store directory is handled
// by the caller).
func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// ---- Mounts ----

// UpsertMount inserts or updates the desired/observed state of a mount.
func (s *Store) UpsertMount(ctx context.Context, m *model.MountState) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mounts(id,agent_id,grant_id,host_path,target_path,mode,status,last_error,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id,target_path) DO UPDATE SET
		grant_id=excluded.grant_id, host_path=excluded.host_path, mode=excluded.mode,
		status=excluded.status, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		m.ID, m.AgentID, m.GrantID, m.HostPath, m.TargetPath, string(m.Mode), string(m.Status), m.LastError, ts(m.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert mount: %w", err)
	}
	return nil
}

// ListMounts returns mount states; agentID empty = all.
func (s *Store) ListMounts(ctx context.Context, agentID string) ([]*model.MountState, error) {
	q := `SELECT id,agent_id,grant_id,host_path,target_path,mode,status,last_error,updated_at FROM mounts`
	var args []any
	if agentID != "" {
		q += ` WHERE agent_id=?`
		args = append(args, agentID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list mounts: %w", err)
	}
	defer rows.Close()
	var out []*model.MountState
	for rows.Next() {
		var m model.MountState
		var ua, mode, status string
		if err := rows.Scan(&m.ID, &m.AgentID, &m.GrantID, &m.HostPath, &m.TargetPath, &mode, &status, &m.LastError, &ua); err != nil {
			return nil, fmt.Errorf("scan mount: %w", err)
		}
		m.Mode = model.AccessMode(mode)
		m.Status = model.MountStatus(status)
		m.UpdatedAt, _ = parseTS(ua)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// DeleteMount removes a mount state row.
func (s *Store) DeleteMount(ctx context.Context, agentID, targetPath string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mounts WHERE agent_id=? AND target_path=?`, agentID, targetPath)
	if err != nil {
		return fmt.Errorf("delete mount: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Events (audit hash chain) ----

// AppendEvent appends an event to the audit log with hash chaining.
func (s *Store) AppendEvent(ctx context.Context, e *model.Event) error {
	var prev string
	_ = s.db.QueryRowContext(ctx, `SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	e.PrevHash = prev
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s|%s", e.ID, e.Type, e.Actor, e.AgentID, e.WorkspaceID, e.Message, ts(e.Timestamp), prev)
	e.Hash = hex.EncodeToString(h.Sum(nil))
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(id,type,actor,agent_id,workspace_id,message,ts,prev_hash,hash)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		e.ID, string(e.Type), e.Actor, e.AgentID, e.WorkspaceID, e.Message, ts(e.Timestamp), e.PrevHash, e.Hash)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents returns the newest events (limit 0 = 200 default).
func (s *Store) ListEvents(ctx context.Context, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,actor,agent_id,workspace_id,message,ts,prev_hash,hash
		FROM events ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []*model.Event
	for rows.Next() {
		var e model.Event
		var typ, tss string
		if err := rows.Scan(&e.ID, &typ, &e.Actor, &e.AgentID, &e.WorkspaceID, &e.Message, &tss, &e.PrevHash, &e.Hash); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.Type = model.EventType(typ)
		e.Timestamp, _ = parseTS(tss)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// VerifyEventChain recomputes the hash chain; returns the first broken seq or 0.
func (s *Store) VerifyEventChain(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,id,type,actor,agent_id,workspace_id,message,ts,prev_hash,hash FROM events ORDER BY seq`)
	if err != nil {
		return 0, fmt.Errorf("verify events: %w", err)
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var seq int64
		var e model.Event
		var typ, tss string
		if err := rows.Scan(&seq, &e.ID, &typ, &e.Actor, &e.AgentID, &e.WorkspaceID, &e.Message, &tss, &e.PrevHash, &e.Hash); err != nil {
			return 0, fmt.Errorf("scan event: %w", err)
		}
		h := sha256.New()
		fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s|%s", e.ID, typ, e.Actor, e.AgentID, e.WorkspaceID, e.Message, tss, prev)
		want := hex.EncodeToString(h.Sum(nil))
		if e.PrevHash != prev || e.Hash != want {
			return seq, nil
		}
		prev = e.Hash
	}
	return 0, rows.Err()
}
