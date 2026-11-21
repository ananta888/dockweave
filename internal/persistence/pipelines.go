package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

const pipelineRevisionKey = "pipeline_revision"

// ErrStaleRevision is returned when a pipeline push carries an older graph
// revision than the stored one.
var ErrStaleRevision = errors.New("stale pipeline revision")

// ErrRevisionConflict means an existing revision was reused with different content.
var ErrRevisionConflict = fmt.Errorf("%w: divergent pipeline revision", ErrConflict)

// ReplacePipelineEdges atomically replaces the whole edge set with the edges
// derived from graph revision rev. An older revision never overwrites a newer
// one (ErrStaleRevision); the same revision is an idempotent re-push. reset
// skips the revision guard (used when the graph document was recreated and
// its revision counter restarted).
func (s *Store) ReplacePipelineEdges(ctx context.Context, rev int, edges []*model.PipelineEdge, reset bool) error {
	return s.ReplacePipelineEdgesAudited(ctx, rev, edges, reset, &model.Event{ID: model.NewID("evt"), Type: model.EventPipelineUpdate, Actor: "system", Message: fmt.Sprintf("pipeline revision %d reset=%t: %d edges", rev, reset, len(edges)), Timestamp: time.Now().UTC()})
}

func (s *Store) ReplacePipelineEdgesAudited(ctx context.Context, rev int, edges []*model.PipelineEdge, reset bool, event *model.Event) error {
	if rev < 0 {
		return fmt.Errorf("pipeline revision must not be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pipeline begin: %w", err)
	}
	defer tx.Rollback()

	current, err := pipelineRevision(ctx, tx)
	if err != nil {
		return err
	}
	if !reset && rev < current {
		return fmt.Errorf("%w: stored %d, pushed %d", ErrStaleRevision, current, rev)
	}
	if !reset && rev == current {
		var initialized int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key=?`, pipelineRevisionKey).Scan(&initialized); err != nil {
			return fmt.Errorf("pipeline initialization: %w", err)
		}
		if initialized > 0 {
			state, err := readPipelineEdges(ctx, tx, PipelineEdgeFilter{})
			if err != nil {
				return err
			}
			if !samePipelineEdges(state.Edges, edges) {
				return fmt.Errorf("%w: revision %d", ErrRevisionConflict, rev)
			}
			return nil
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pipeline_edges`); err != nil {
		return fmt.Errorf("clear pipeline edges: %w", err)
	}
	now := time.Now().UTC()
	for _, e := range edges {
		e.SourceRevision = rev
		e.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_edges(
			id,gate_id,upstream_agent_id,downstream_agent_id,workspace,on_true,on_false,
			reason_true,reason_false,max_age_sec,source_revision,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.ID, e.GateID, e.UpstreamAgentID, e.DownstreamAgentID, e.Workspace, e.OnTrue, e.OnFalse,
			e.ReasonTrue, e.ReasonFalse, e.MaxAgeSec, rev, ts(now)); err != nil {
			if isUniqueErr(err) {
				return fmt.Errorf("%w: duplicate pipeline edge %s (%s -> %s)", ErrConflict, e.ID, e.GateID, e.DownstreamAgentID)
			}
			return fmt.Errorf("insert pipeline edge: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, pipelineRevisionKey, strconv.Itoa(rev)); err != nil {
		return fmt.Errorf("set pipeline revision: %w", err)
	}
	if event == nil {
		return errors.New("pipeline update requires audit event")
	}
	if err := appendEventTx(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pipeline commit: %w", err)
	}
	return nil
}

func samePipelineEdges(a, b []*model.PipelineEdge) bool {
	if len(a) != len(b) {
		return false
	}
	byID := make(map[string]model.PipelineEdge, len(a))
	for _, e := range a {
		if e == nil {
			return false
		}
		c := *e
		c.SourceRevision = 0
		c.UpdatedAt = time.Time{}
		byID[c.ID] = c
	}
	for _, e := range b {
		if e == nil {
			return false
		}
		c := *e
		c.SourceRevision = 0
		c.UpdatedAt = time.Time{}
		old, ok := byID[c.ID]
		if !ok || old != c {
			return false
		}
		delete(byID, c.ID)
	}
	return len(byID) == 0
}

func pipelineRevision(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, pipelineRevisionKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read pipeline revision: %w", err)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid pipeline revision %q: %w", v, err)
	}
	return n, nil
}

// PipelineEdgeFilter narrows ListPipelineEdges; empty fields match all.
type PipelineEdgeFilter struct {
	DownstreamAgentID string
	UpstreamAgentID   string
	Workspace         string
}

// ListPipelineEdges returns the stored state (revision + matching edges).
func (s *Store) ListPipelineEdges(ctx context.Context, f PipelineEdgeFilter) (*model.PipelineState, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("pipeline snapshot begin: %w", err)
	}
	defer tx.Rollback()
	state, err := readPipelineEdges(ctx, tx, f)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("pipeline snapshot commit: %w", err)
	}
	return state, nil
}

func readPipelineEdges(ctx context.Context, tx *sql.Tx, f PipelineEdgeFilter) (*model.PipelineState, error) {
	rev, err := pipelineRevision(ctx, tx)
	if err != nil {
		return nil, err
	}
	var (
		where []string
		args  []any
	)
	if f.DownstreamAgentID != "" {
		where = append(where, "downstream_agent_id=?")
		args = append(args, f.DownstreamAgentID)
	}
	if f.UpstreamAgentID != "" {
		where = append(where, "upstream_agent_id=?")
		args = append(args, f.UpstreamAgentID)
	}
	if f.Workspace != "" {
		where = append(where, "workspace=?")
		args = append(args, f.Workspace)
	}
	q := `SELECT id,gate_id,upstream_agent_id,downstream_agent_id,workspace,on_true,on_false,
		reason_true,reason_false,max_age_sec,source_revision,updated_at FROM pipeline_edges`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY downstream_agent_id, gate_id, id"
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list pipeline edges: %w", err)
	}
	defer rows.Close()
	state := &model.PipelineState{Revision: rev, Edges: []*model.PipelineEdge{}}
	for rows.Next() {
		e := &model.PipelineEdge{}
		var updated string
		if err := rows.Scan(&e.ID, &e.GateID, &e.UpstreamAgentID, &e.DownstreamAgentID, &e.Workspace,
			&e.OnTrue, &e.OnFalse, &e.ReasonTrue, &e.ReasonFalse, &e.MaxAgeSec, &e.SourceRevision, &updated); err != nil {
			return nil, fmt.Errorf("scan pipeline edge: %w", err)
		}
		e.UpdatedAt, _ = parseTS(updated)
		if e.UpdatedAt.After(state.UpdatedAt) {
			state.UpdatedAt = e.UpdatedAt
		}
		state.Edges = append(state.Edges, e)
	}
	return state, rows.Err()
}

// EdgesForDownstream returns the edges that gate the given agent.
func (s *Store) EdgesForDownstream(ctx context.Context, agentID string) ([]*model.PipelineEdge, int, error) {
	st, err := s.ListPipelineEdges(ctx, PipelineEdgeFilter{DownstreamAgentID: agentID})
	if err != nil {
		return nil, 0, err
	}
	return st.Edges, st.Revision, nil
}

// RecentGateRuns returns up to limit runs of a workspace, newest first. The
// order is computed from the parsed timestamps (RFC3339Nano strings do not
// sort lexically when the fraction has a varying number of digits).
func (s *Store) RecentGateRuns(ctx context.Context, workspace string, limit int) ([]*model.GateRun, error) {
	runs, err := s.ListGateRuns(ctx, workspace, 500)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}
		return runs[i].ID > runs[j].ID
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

// CreatePipelineOverride stores an override.
func (s *Store) CreatePipelineOverride(ctx context.Context, o *model.PipelineOverride) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("override begin: %w", err)
	}
	defer tx.Rollback()
	single := 0
	if o.SingleUse {
		single = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_overrides(
		id,agent_id,gate_id,reason,actor,single_use,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`,
		o.ID, o.AgentID, o.GateID, o.Reason, o.Actor, single, ts(o.CreatedAt), ts(o.ExpiresAt))
	if err != nil {
		return fmt.Errorf("create pipeline override: %w", err)
	}
	if err := appendEventTx(ctx, tx, &model.Event{ID: model.NewID("evt"), Type: model.EventPipelineOverride, Actor: o.Actor, AgentID: o.AgentID, Message: fmt.Sprintf("override %s gate %s expires %s single_use=%t: %s", o.ID, o.GateID, ts(o.ExpiresAt), o.SingleUse, o.Reason), Timestamp: o.CreatedAt}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create override commit: %w", err)
	}
	return nil
}

// ListPipelineOverrides returns overrides for an agent (all agents when
// empty), newest first. activeAt filters to overrides usable at that time
// (zero = no filter).
func (s *Store) ListPipelineOverrides(ctx context.Context, agentID string, activeAt time.Time) ([]*model.PipelineOverride, error) {
	q := `SELECT id,agent_id,gate_id,reason,actor,single_use,created_at,expires_at,used_at FROM pipeline_overrides`
	var args []any
	if agentID != "" {
		q += ` WHERE agent_id=?`
		args = append(args, agentID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list pipeline overrides: %w", err)
	}
	defer rows.Close()
	var out []*model.PipelineOverride
	for rows.Next() {
		o := &model.PipelineOverride{}
		var single int
		var created, expires string
		var used sql.NullString
		if err := rows.Scan(&o.ID, &o.AgentID, &o.GateID, &o.Reason, &o.Actor, &single, &created, &expires, &used); err != nil {
			return nil, fmt.Errorf("scan pipeline override: %w", err)
		}
		o.SingleUse = single == 1
		o.CreatedAt, _ = parseTS(created)
		o.ExpiresAt, _ = parseTS(expires)
		if used.Valid && used.String != "" {
			t, _ := parseTS(used.String)
			o.UsedAt = &t
		}
		if !activeAt.IsZero() && !o.Active(activeAt) {
			continue
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ConsumePipelineOverride records the use of an override. It fails with
// ErrConflict when the override has expired or a single-use override was
// already used; the check and the update run in one transaction, so two
// concurrent requests can never both consume the same single-use override.
func (s *Store) ConsumePipelineOverride(ctx context.Context, id string, at time.Time) error {
	return s.ConsumePipelineOverrides(ctx, []string{id}, at)
}

// ConsumePipelineOverrides atomically consumes each distinct authorization and
// records one audit event per authorization. Failure rolls back every use.
func (s *Store) ConsumePipelineOverrides(ctx context.Context, ids []string, at time.Time) error {
	return s.ConsumePipelineOverridesAudited(ctx, ids, at, "pipeline", "")
}

func (s *Store) ConsumePipelineOverridesAudited(ctx context.Context, ids []string, at time.Time, actor, issue string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("consume override begin: %w", err)
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var single int
		var expires, agent, author string
		var used sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT single_use,expires_at,used_at,agent_id,actor FROM pipeline_overrides WHERE id=?`, id).
			Scan(&single, &expires, &used, &agent, &author)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: override %s", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("read pipeline override: %w", err)
		}
		exp, err := parseTS(expires)
		if err != nil || !at.Before(exp) {
			return fmt.Errorf("%w: override %s has expired", ErrConflict, id)
		}
		if single == 1 && used.Valid && used.String != "" {
			return fmt.Errorf("%w: override %s was already used", ErrConflict, id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_overrides SET used_at=? WHERE id=?`, ts(at), id); err != nil {
			return fmt.Errorf("consume pipeline override: %w", err)
		}
		if err := appendEventTx(ctx, tx, &model.Event{ID: model.NewID("evt"), Type: model.EventPipelineOverrideUsed, Actor: actor, AgentID: agent, Message: fmt.Sprintf("override %s authorized by %s consumed for one work request (issue %s)", id, author, issue), Timestamp: at}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("consume override commit: %w", err)
	}
	return nil
}
