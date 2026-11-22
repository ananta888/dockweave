package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ananta888/dockweave/internal/model"
)

// GetDispatchAction returns the marker for key; a missing key is the zero
// value (no error).
func (s *Store) GetDispatchAction(ctx context.Context, key string) (model.DispatchAction, error) {
	var a model.DispatchAction
	var since, updated string
	err := s.db.QueryRowContext(ctx, `SELECT key, status, since, result, updated_at FROM dispatch_actions WHERE key=?`, key).
		Scan(&a.Key, &a.Status, &since, &a.Result, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DispatchAction{Key: key}, nil
	}
	if err != nil {
		return a, fmt.Errorf("get dispatch action: %w", err)
	}
	a.Since, a.UpdatedAt = parseTime(since), parseTime(updated)
	return a, nil
}

// PutDispatchAction stores the marker durably (the write is committed
// before the side effect it guards is attempted).
func (s *Store) PutDispatchAction(ctx context.Context, a model.DispatchAction) error {
	if a.Key == "" || a.Status == "" {
		return errors.New("dispatch action needs key and status")
	}
	since := a.Since.UTC().Format("2006-01-02T15:04:05.000000000Z")
	_, err := s.db.ExecContext(ctx, `INSERT INTO dispatch_actions (key, status, since, result, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET status=excluded.status, since=excluded.since, result=excluded.result, updated_at=excluded.updated_at`,
		a.Key, a.Status, since, a.Result, nowString())
	if err != nil {
		return fmt.Errorf("put dispatch action: %w", err)
	}
	return nil
}
