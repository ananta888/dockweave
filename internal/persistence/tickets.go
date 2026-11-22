package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// TaskQuery filters tasks for the native ticketing adapter. Zero values mean
// "any".
type TaskQuery struct {
	Statuses     []string
	AssigneeKind string // human | agent
	AssigneeID   string // agent id (kind agent) or human principal (kind human)
	ParentID     string
	UpdatedSince time.Time
	Query        string // case-insensitive substring of title or description
	// Unclaimed restricts to tasks nobody has checked out.
	Unclaimed bool
	// InboxOrder sorts by priority (high first), then age; default is age.
	InboxOrder bool
	Limit      int
}

// ListTicketTasks returns tasks matching q.
func (s *Store) ListTicketTasks(ctx context.Context, q TaskQuery) ([]*model.Task, error) {
	sqlq := `SELECT ` + taskColumns + ` FROM tasks WHERE 1=1`
	var args []any
	if len(q.Statuses) > 0 {
		sqlq += ` AND status IN (?` + strings.Repeat(",?", len(q.Statuses)-1) + `)`
		for _, st := range q.Statuses {
			args = append(args, st)
		}
	}
	if q.AssigneeKind != "" {
		sqlq += ` AND assignee_kind = ?`
		args = append(args, q.AssigneeKind)
	}
	if q.AssigneeID != "" {
		if q.AssigneeKind == string(model.AssigneeAgent) {
			sqlq += ` AND agent_id = ?`
		} else {
			sqlq += ` AND assignee_id = ?`
		}
		args = append(args, q.AssigneeID)
	}
	if q.ParentID != "" {
		sqlq += ` AND parent_id = ?`
		args = append(args, q.ParentID)
	}
	if !q.UpdatedSince.IsZero() {
		// Stored timestamps are RFC3339Nano (not fixed width): compare as time.
		sqlq += ` AND julianday(updated_at) >= julianday(?)`
		args = append(args, q.UpdatedSince.UTC().Format(time.RFC3339Nano))
	}
	if needle := strings.TrimSpace(q.Query); needle != "" {
		sqlq += ` AND (instr(lower(title), lower(?)) > 0 OR instr(lower(description), lower(?)) > 0)`
		args = append(args, needle, needle)
	}
	if q.Unclaimed {
		sqlq += ` AND checked_out_by = ''`
	}
	if q.InboxOrder {
		sqlq += ` ORDER BY priority DESC, julianday(created_at) ASC, ticket_seq ASC`
	} else {
		sqlq += ` ORDER BY julianday(created_at) ASC, ticket_seq ASC`
	}
	if q.Limit > 0 {
		sqlq += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("list ticket tasks: %w", err)
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

// CheckoutTask claims a task for agentID. The claim is a single conditional
// UPDATE, so concurrent callers (also across processes) have exactly one
// winner. Re-claiming by the holder is a no-op; another holder is
// ErrConflict.
func (s *Store) CheckoutTask(ctx context.Context, id, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("%w: empty agent", ErrConflict)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET checked_out_by=?, checked_out_at=?
		WHERE id=? AND (checked_out_by='' OR checked_out_by=?)`, agentID, nowString(), id, agentID)
	if err != nil {
		return fmt.Errorf("checkout task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	t, err := s.GetTask(ctx, id)
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: task %s is checked out by %s", ErrConflict, id, t.CheckedOutBy)
}

// ReleaseTask gives a claimed task back; only the holder may release it.
func (s *Store) ReleaseTask(ctx context.Context, id, agentID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET checked_out_by='', checked_out_at=NULL
		WHERE id=? AND checked_out_by=? AND checked_out_by<>''`, id, agentID)
	if err != nil {
		return fmt.Errorf("release task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, err := s.GetTask(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("%w: task %s is not checked out by %s", ErrConflict, id, agentID)
}

// AddTicketComment appends a comment to a task's thread.
func (s *Store) AddTicketComment(ctx context.Context, c *model.TicketComment) error {
	if strings.TrimSpace(c.Body) == "" {
		return errors.New("comment body must not be empty")
	}
	if c.ID == "" {
		c.ID = model.NewID("tc")
	}
	stamp := nowString()
	c.CreatedAt = parseTime(stamp)
	if c.Origin == "" {
		c.Origin = "native:" + c.ID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO ticket_comments
		(id, task_id, author_kind, author_id, author_name, body, origin, created_at)
		SELECT ?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM tasks WHERE id=?)`,
		c.ID, c.TaskID, c.AuthorKind, c.AuthorID, c.AuthorName, c.Body, c.Origin, stamp, c.TaskID)
	if err != nil {
		return fmt.Errorf("add ticket comment: %w", err)
	}
	// The INSERT … SELECT writes nothing for a missing task.
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM ticket_comments WHERE id=?`, c.ID).Scan(&n); err != nil {
		return fmt.Errorf("add ticket comment: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTicketComments returns a task's thread in order.
func (s *Store) ListTicketComments(ctx context.Context, taskID string) ([]model.TicketComment, error) {
	if _, err := s.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, author_kind, author_id, author_name, body, origin, created_at
		FROM ticket_comments WHERE task_id=? ORDER BY julianday(created_at) ASC, rowid ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list ticket comments: %w", err)
	}
	defer rows.Close()
	var out []model.TicketComment
	for rows.Next() {
		var c model.TicketComment
		var created string
		if err := rows.Scan(&c.ID, &c.TaskID, &c.AuthorKind, &c.AuthorID, &c.AuthorName, &c.Body, &c.Origin, &created); err != nil {
			return nil, fmt.Errorf("scan ticket comment: %w", err)
		}
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}
