package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Productivity layer: objectives, daily tasks, focus sessions, work events,
// evidence, budgets and reviews. All timestamps are stored as RFC3339.

func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func encodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func decodeStrings(raw string) []string {
	var out []string
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func decodeMap(raw string) map[string]string {
	var out map[string]string
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// CreateObjective stores a goal hierarchy node.
func (s *Store) CreateObjective(ctx context.Context, o *model.Objective) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if o.ID == "" {
		o.ID = model.NewID("ob")
	}
	now := time.Now().UTC()
	o.CreatedAt, o.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO objectives (id, kind, parent_id, title, description, status,
			period_start, period_end, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.Kind, o.ParentID, o.Title, o.Description, o.Status,
		o.PeriodStart, o.PeriodEnd, nowString(), nowString())
	if err != nil {
		return fmt.Errorf("create objective: %w", err)
	}
	return nil
}

// ListObjectives returns all goals, newest periods first.
func (s *Store) ListObjectives(ctx context.Context) ([]*model.Objective, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, parent_id, title, description, status, period_start,
		       period_end, created_at, updated_at
		FROM objectives ORDER BY kind, period_start, title`)
	if err != nil {
		return nil, fmt.Errorf("list objectives: %w", err)
	}
	defer rows.Close()
	var out []*model.Objective
	for rows.Next() {
		var o model.Objective
		var created, updated string
		if err := rows.Scan(&o.ID, &o.Kind, &o.ParentID, &o.Title, &o.Description,
			&o.Status, &o.PeriodStart, &o.PeriodEnd, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan objective: %w", err)
		}
		o.CreatedAt, o.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, &o)
	}
	return out, rows.Err()
}

// UpdateObjectiveStatus changes the lifecycle state of an objective.
func (s *Store) UpdateObjectiveStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE objectives SET status = ?, updated_at = ? WHERE id = ?`,
		status, nowString(), id)
	if err != nil {
		return fmt.Errorf("update objective: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateTask stores a task on the daily queue or backlog.
func (s *Store) CreateTask(ctx context.Context, t *model.Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = model.NewID("tk")
	}
	now := time.Now().UTC()
	t.CreatedAt, t.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, title, description, notes, objective_ids,
			assignee_kind, agent_id, workspace_id, day, status, priority, risk,
			effort_minutes, order_index, blocked_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Description, t.Notes, encodeJSON(t.ObjectiveIDs),
		t.AssigneeKind, t.AgentID, t.WorkspaceID, t.Day, t.Status, t.Priority,
		t.Risk, t.EffortMinutes, t.OrderIndex, encodeJSON(t.BlockedBy),
		nowString(), nowString())
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func scanTask(scan func(dest ...any) error) (*model.Task, error) {
	var t model.Task
	var objectives, blocked, created, updated string
	var completed sql.NullString
	if err := scan(&t.ID, &t.Title, &t.Description, &t.Notes, &objectives,
		&t.AssigneeKind, &t.AgentID, &t.WorkspaceID, &t.Day, &t.Status,
		&t.Priority, &t.Risk, &t.EffortMinutes, &t.OrderIndex, &blocked,
		&created, &updated, &completed); err != nil {
		return nil, err
	}
	t.ObjectiveIDs = decodeStrings(objectives)
	t.BlockedBy = decodeStrings(blocked)
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	t.CompletedAt = parseTimePtr(completed)
	return &t, nil
}

const taskColumns = `id, title, description, notes, objective_ids, assignee_kind,
	agent_id, workspace_id, day, status, priority, risk, effort_minutes,
	order_index, blocked_by, created_at, updated_at, completed_at`

// GetTask returns one task or ErrNotFound.
func (s *Store) GetTask(ctx context.Context, id string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

// ListTasks filters by day, status and assignee kind (empty = any).
func (s *Store) ListTasks(ctx context.Context, day, status, assignee string, limit int) ([]*model.Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks WHERE 1=1`
	args := []any{}
	if day != "" {
		q += ` AND day = ?`
		args = append(args, day)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if assignee != "" {
		q += ` AND assignee_kind = ?`
		args = append(args, assignee)
	}
	q += ` ORDER BY day DESC, order_index ASC, priority DESC, created_at ASC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var out []*model.Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTask rewrites a task and stamps completed_at when it is finished.
func (s *Store) UpdateTask(ctx context.Context, t *model.Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	var completed any
	if t.IsFinished() {
		if t.CompletedAt == nil {
			now := time.Now().UTC()
			t.CompletedAt = &now
		}
		completed = t.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET title=?, description=?, notes=?, objective_ids=?,
			assignee_kind=?, agent_id=?, workspace_id=?, day=?, status=?,
			priority=?, risk=?, effort_minutes=?, order_index=?, blocked_by=?,
			updated_at=?, completed_at=?
		WHERE id = ?`,
		t.Title, t.Description, t.Notes, encodeJSON(t.ObjectiveIDs),
		t.AssigneeKind, t.AgentID, t.WorkspaceID, t.Day, t.Status, t.Priority,
		t.Risk, t.EffortMinutes, t.OrderIndex, encodeJSON(t.BlockedBy),
		nowString(), completed, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTask removes a task and its focus sessions.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM focus_sessions WHERE task_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

const focusColumns = `id, task_id, human_id, status, started_at, ended_at, paused_seconds, notes`

func scanFocus(scan func(dest ...any) error) (*model.FocusSession, error) {
	var f model.FocusSession
	var started string
	var ended sql.NullString
	if err := scan(&f.ID, &f.TaskID, &f.HumanID, &f.Status, &started, &ended,
		&f.PausedSecond, &f.Notes); err != nil {
		return nil, err
	}
	f.StartedAt = parseTime(started)
	f.EndedAt = parseTimePtr(ended)
	return &f, nil
}

// StartFocus opens the single focus session a human may have. A task can be
// focused by only one human at a time.
func (s *Store) StartFocus(ctx context.Context, taskID, humanID string) (*model.FocusSession, error) {
	if humanID == "" {
		return nil, errors.New("human id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM focus_sessions WHERE human_id = ? AND status IN ('active','paused')`,
		humanID).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, ErrConflict
	}

	var taskStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&taskStatus); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if taskStatus == string(model.TaskDone) || taskStatus == string(model.TaskVerified) {
		return nil, fmt.Errorf("%w: task is already finished", ErrConflict)
	}

	now := time.Now().UTC()
	f := &model.FocusSession{
		ID: model.NewID("fs"), TaskID: taskID, HumanID: humanID,
		Status: model.FocusActive, StartedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO focus_sessions (id, task_id, human_id, status, started_at, paused_seconds, notes)
		VALUES (?,?,?,?,?,0,'')`,
		f.ID, f.TaskID, f.HumanID, f.Status, now.Format(time.RFC3339Nano)); err != nil {
		return nil, fmt.Errorf("start focus: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
		model.TaskInProgress, nowString(), taskID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

// CurrentFocus returns the active or paused session of a human.
func (s *Store) CurrentFocus(ctx context.Context, humanID string) (*model.FocusSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+focusColumns+` FROM focus_sessions
		 WHERE human_id = ? AND status IN ('active','paused')`, humanID)
	f, err := scanFocus(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("current focus: %w", err)
	}
	return f, nil
}

// SetFocusStatus pauses or resumes the current session.
func (s *Store) SetFocusStatus(ctx context.Context, humanID, status string) (*model.FocusSession, error) {
	if status != string(model.FocusPaused) && status != string(model.FocusActive) {
		return nil, errors.New("status must be paused or active")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE focus_sessions SET status = ? WHERE human_id = ? AND status IN ('active','paused')`,
		status, humanID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.CurrentFocus(ctx, humanID)
}

// EndFocus completes or skips the session, records a human_focus event and
// closes the task when it was completed.
func (s *Store) EndFocus(ctx context.Context, humanID string, complete bool, notes string) (*model.FocusSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`SELECT `+focusColumns+` FROM focus_sessions
		 WHERE human_id = ? AND status IN ('active','paused')`, humanID)
	f, err := scanFocus(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	status := model.FocusSkipped
	if complete {
		status = model.FocusCompleted
	}
	if notes != "" {
		f.Notes = notes
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE focus_sessions SET status=?, ended_at=?, notes=? WHERE id=?`,
		status, now.Format(time.RFC3339Nano), f.Notes, f.ID); err != nil {
		return nil, err
	}

	if complete {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET status=?, completed_at=?, updated_at=? WHERE id=?`,
			model.TaskDone, now.Format(time.RFC3339Nano), nowString(), f.TaskID); err != nil {
			return nil, err
		}
	} else {
		// Skipping puts the task back into the queue instead of leaving it
		// stuck in_progress.
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET status=?, updated_at=? WHERE id=? AND status=?`,
			model.TaskTodo, nowString(), f.TaskID, model.TaskInProgress); err != nil {
			return nil, err
		}
	}

	elapsed := int64(f.Elapsed(now).Seconds())
	ev := &model.WorkEvent{
		ID: model.NewID("ev"), Kind: model.EventHumanFocus, TaskID: f.TaskID,
		HumanID: humanID, StartedAt: f.StartedAt, EndedAt: &now,
		DurationSeconds: elapsed, Source: "focus",
		IdempotencyKey: "focus:" + f.ID,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_events (id, kind, task_id, objective_ids, agent_id, human_id,
			started_at, ended_at, duration_seconds, source, meta, idempotency_key, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.ID, ev.Kind, ev.TaskID, "[]", "", ev.HumanID,
		ev.StartedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		elapsed, ev.Source, "{}", ev.IdempotencyKey, nowString()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	f.Status, f.EndedAt = status, &now
	return f, nil
}

// AddWorkEvent stores an idempotent measurement. The bool reports whether it
// was inserted (false = duplicate key).
func (s *Store) AddWorkEvent(ctx context.Context, e *model.WorkEvent) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	if e.ID == "" {
		e.ID = model.NewID("ev")
	}
	e.CreatedAt = time.Now().UTC()
	var ended any
	if e.EndedAt != nil {
		ended = e.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	var key any
	if e.IdempotencyKey != "" {
		key = e.IdempotencyKey
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO work_events (id, kind, task_id, objective_ids, agent_id, human_id,
			started_at, ended_at, duration_seconds, source, meta, idempotency_key, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(idempotency_key) DO NOTHING`,
		e.ID, e.Kind, e.TaskID, encodeJSON(e.ObjectiveIDs), e.AgentID, e.HumanID,
		e.StartedAt.UTC().Format(time.RFC3339Nano), ended, e.DurationSeconds,
		e.Source, encodeJSON(e.Meta), key, nowString())
	if err != nil {
		return false, fmt.Errorf("add work event: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListWorkEvents returns measurements, newest first.
func (s *Store) ListWorkEvents(ctx context.Context, taskID string, limit int) ([]*model.WorkEvent, error) {
	q := `SELECT id, kind, task_id, objective_ids, agent_id, human_id, started_at,
		ended_at, duration_seconds, source, meta, idempotency_key, created_at
		FROM work_events`
	args := []any{}
	if taskID != "" {
		q += ` WHERE task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY started_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list work events: %w", err)
	}
	defer rows.Close()
	var out []*model.WorkEvent
	for rows.Next() {
		var e model.WorkEvent
		var started, created, objectives, meta string
		var ended, key sql.NullString
		if err := rows.Scan(&e.ID, &e.Kind, &e.TaskID, &objectives, &e.AgentID,
			&e.HumanID, &started, &ended, &e.DurationSeconds, &e.Source, &meta,
			&key, &created); err != nil {
			return nil, err
		}
		e.ObjectiveIDs = decodeStrings(objectives)
		e.StartedAt = parseTime(started)
		e.EndedAt = parseTimePtr(ended)
		e.Meta = decodeMap(meta)
		e.IdempotencyKey = key.String
		e.CreatedAt = parseTime(created)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// AddEvidence stores a verifiable result idempotently.
func (s *Store) AddEvidence(ctx context.Context, e *model.Evidence) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	if e.ID == "" {
		e.ID = model.NewID("evd")
	}
	e.CreatedAt = time.Now().UTC()
	var key any
	if e.IdempotencyKey != "" {
		key = e.IdempotencyKey
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO evidence (id, task_id, kind, status, ref, verified, payload, idempotency_key, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(idempotency_key) DO NOTHING`,
		e.ID, e.TaskID, e.Kind, e.Status, e.Ref, e.Verified, encodeJSON(e.Payload),
		key, nowString())
	if err != nil {
		return false, fmt.Errorf("add evidence: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.refreshTaskVerification(ctx, e.TaskID)
	}
	return n > 0, nil
}

// refreshTaskVerification promotes a done task to verified when a passing,
// verified evidence row exists. Agent statements alone never do this.
func (s *Store) refreshTaskVerification(ctx context.Context, taskID string) {
	var passed int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM evidence WHERE task_id = ? AND verified = 1 AND status = 'passed'`,
		taskID).Scan(&passed)
	if passed == 0 {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		model.TaskVerified, nowString(), taskID, model.TaskDone)
}

// ListEvidence returns the evidence of a task.
func (s *Store) ListEvidence(ctx context.Context, taskID string) ([]*model.Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, kind, status, ref, verified, payload, idempotency_key, created_at
		FROM evidence WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	defer rows.Close()
	var out []*model.Evidence
	for rows.Next() {
		var e model.Evidence
		var payload, created string
		var verified int
		var key sql.NullString
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Kind, &e.Status, &e.Ref,
			&verified, &payload, &key, &created); err != nil {
			return nil, err
		}
		e.Verified = verified == 1
		e.Payload = decodeMap(payload)
		e.IdempotencyKey = key.String
		e.CreatedAt = parseTime(created)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// UpsertBudget creates or updates an input budget.
func (s *Store) UpsertBudget(ctx context.Context, b *model.InputBudget) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.ID == "" {
		b.ID = model.NewID("bd")
	}
	now := time.Now().UTC()
	b.CreatedAt, b.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO input_budgets (id, scope, scope_id, kind, period, target, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(scope, scope_id, kind, period) DO UPDATE SET
			target = excluded.target, updated_at = excluded.updated_at`,
		b.ID, b.Scope, b.ScopeID, b.Kind, b.Period, b.Target, nowString(), nowString())
	if err != nil {
		return fmt.Errorf("upsert budget: %w", err)
	}
	return nil
}

// ListBudgets returns all input budgets.
func (s *Store) ListBudgets(ctx context.Context) ([]*model.InputBudget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scope, scope_id, kind, period, target, created_at, updated_at
		 FROM input_budgets ORDER BY scope, kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.InputBudget
	for rows.Next() {
		var b model.InputBudget
		var created, updated string
		if err := rows.Scan(&b.ID, &b.Scope, &b.ScopeID, &b.Kind, &b.Period,
			&b.Target, &created, &updated); err != nil {
			return nil, err
		}
		b.CreatedAt, b.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, &b)
	}
	return out, rows.Err()
}

// CreateReview stores a reflection snapshot.
func (s *Store) CreateReview(ctx context.Context, r *model.Review) error {
	if r.Kind != model.ReviewWeekly && r.Kind != model.ReviewMonthly {
		return fmt.Errorf("unsupported review kind %q", r.Kind)
	}
	if r.ID == "" {
		r.ID = model.NewID("rv")
	}
	r.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reviews (id, kind, period_start, period_end, body, created_at)
		VALUES (?,?,?,?,?,?)`,
		r.ID, r.Kind, r.PeriodStart, r.PeriodEnd, encodeJSON(r.Body), nowString())
	if err != nil {
		return fmt.Errorf("create review: %w", err)
	}
	return nil
}

// ListReviews returns reviews, newest first.
func (s *Store) ListReviews(ctx context.Context, limit int) ([]*model.Review, error) {
	q := `SELECT id, kind, period_start, period_end, body, created_at FROM reviews ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Review
	for rows.Next() {
		var r model.Review
		var body, created string
		if err := rows.Scan(&r.ID, &r.Kind, &r.PeriodStart, &r.PeriodEnd, &body, &created); err != nil {
			return nil, err
		}
		r.Body = map[string]any{}
		_ = json.Unmarshal([]byte(body), &r.Body)
		r.CreatedAt = parseTime(created)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// TodayPlan returns the deterministic daily queue for one human.
func (s *Store) TodayPlan(ctx context.Context, day, humanID string) (*model.TodayPlan, error) {
	tasks, err := s.ListTasks(ctx, day, "", "", 0)
	if err != nil {
		return nil, err
	}
	plan := &model.TodayPlan{Day: day, Tasks: tasks}
	if humanID != "" {
		if f, err := s.CurrentFocus(ctx, humanID); err == nil {
			plan.Focus = f
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	var active int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE assignee_kind = 'agent' AND status = 'in_progress'`,
	).Scan(&active); err == nil {
		plan.ActiveAgent = active
	}
	return plan, nil
}

// AgentRuntimeIntervals derives container runtime per agent from the audit
// log (started/stopped events). Nothing is stored, so nothing can be counted
// twice; an open interval ends at `now` and is marked as running.
func (s *Store) AgentRuntimeIntervals(ctx context.Context, since time.Time) ([]*model.WorkEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT type, agent_id, ts FROM events
		WHERE agent_id != '' AND ts >= ? AND type IN ('agent.started','agent.stopped')
		ORDER BY ts ASC, type ASC`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("read runtime events: %w", err)
	}
	defer rows.Close()

	type open struct{ start time.Time }
	active := map[string]*open{}
	var out []*model.WorkEvent
	now := time.Now().UTC()

	for rows.Next() {
		var typ, agent, ts string
		if err := rows.Scan(&typ, &agent, &ts); err != nil {
			return nil, err
		}
		t := parseTime(ts)
		switch typ {
		case "agent.started":
			active[agent] = &open{start: t}
		case "agent.stopped":
			if o, ok := active[agent]; ok {
				out = append(out, runtimeEvent(agent, o.start, t, "stopped"))
				delete(active, agent)
			}
		}
	}
	for agent, o := range active {
		out = append(out, runtimeEvent(agent, o.start, now, "running"))
	}
	return out, rows.Err()
}

func runtimeEvent(agent string, start, end time.Time, state string) *model.WorkEvent {
	d := end.Sub(start)
	if d < 0 {
		d = 0
	}
	return &model.WorkEvent{
		ID: model.NewID("ev"), Kind: model.EventContainer, AgentID: agent,
		StartedAt: start, EndedAt: &end, DurationSeconds: int64(d.Seconds()),
		Source: "derived:audit", Meta: map[string]string{"state": state},
	}
}

// RolloverTasks moves unfinished human tasks from one day to another and
// returns how many were moved. Finished tasks and agent orders stay untouched.
func (s *Store) RolloverTasks(ctx context.Context, from, to string) (int, error) {
	q := `UPDATE tasks SET day = ?, updated_at = ? WHERE day = ? AND assignee_kind = 'human'
		AND status NOT IN ('done','verified','cancelled')`
	res, err := s.db.ExecContext(ctx, q, to, nowString(), from)
	if err != nil {
		return 0, fmt.Errorf("rollover tasks: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneProductivity deletes measurements, evidence, reviews and closed focus
// sessions older than the given time. Returns the number of removed rows.
func (s *Store) PruneProductivity(ctx context.Context, before time.Time) (map[string]int64, error) {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	out := map[string]int64{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for name, stmt := range map[string]string{
		"work_events":  `DELETE FROM work_events WHERE started_at < ?`,
		"evidence":     `DELETE FROM evidence WHERE created_at < ?`,
		"reviews":      `DELETE FROM reviews WHERE created_at < ?`,
		"focus_closed": `DELETE FROM focus_sessions WHERE status IN ('completed','skipped') AND started_at < ?`,
	} {
		res, err := tx.ExecContext(ctx, stmt, cutoff)
		if err != nil {
			return nil, fmt.Errorf("prune %s: %w", name, err)
		}
		n, _ := res.RowsAffected()
		out[name] = n
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
