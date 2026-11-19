package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ananta888/dockweave/internal/model"
)

type gateRunDetail struct {
	Reason   string             `json:"reason,omitempty"`
	Results  []model.GateResult `json:"results,omitempty"`
	Diff     *model.GateDiff    `json:"diff,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
}

// SaveGateRun inserts or replaces a gate run (idempotent by id).
func (s *Store) SaveGateRun(ctx context.Context, r *model.GateRun) error {
	detail, err := json.Marshal(gateRunDetail{
		Reason: r.Reason, Results: r.Results, Diff: r.Diff, Warnings: r.Warnings,
	})
	if err != nil {
		return fmt.Errorf("encode gate run: %w", err)
	}
	tamper := 0
	if r.Tamper {
		tamper = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gate_runs(
		id,workspace,image,image_digest,status,gates,passed,duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status, gates=excluded.gates, passed=excluded.passed,
			duration_ms=excluded.duration_ms, gates_hash=excluded.gates_hash,
			tamper=excluded.tamper, detail=excluded.detail, finished_at=excluded.finished_at`,
		r.ID, r.Workspace, r.Image, r.ImageDigest, string(r.Status), r.Gates, r.Passed,
		r.DurationMS, r.GatesHash, tamper, r.Actor, string(detail), ts(r.StartedAt), ts(r.FinishedAt))
	if err != nil {
		return fmt.Errorf("save gate run: %w", err)
	}
	return nil
}

// ListGateRuns returns the newest runs, optionally filtered by workspace.
func (s *Store) ListGateRuns(ctx context.Context, workspace string, limit int) ([]*model.GateRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if workspace == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT id,workspace,image,image_digest,status,gates,passed,
			duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at
			FROM gate_runs ORDER BY started_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id,workspace,image,image_digest,status,gates,passed,
			duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at
			FROM gate_runs WHERE workspace=? ORDER BY started_at DESC LIMIT ?`, workspace, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list gate runs: %w", err)
	}
	defer rows.Close()
	out := make([]*model.GateRun, 0, limit)
	for rows.Next() {
		r := &model.GateRun{}
		var status, detail, started, finished string
		var tamper int
		if err := rows.Scan(&r.ID, &r.Workspace, &r.Image, &r.ImageDigest, &status, &r.Gates, &r.Passed,
			&r.DurationMS, &r.GatesHash, &tamper, &r.Actor, &detail, &started, &finished); err != nil {
			return nil, fmt.Errorf("scan gate run: %w", err)
		}
		r.Status = model.GateRunStatus(status)
		r.Tamper = tamper == 1
		r.StartedAt, _ = parseTS(started)
		r.FinishedAt, _ = parseTS(finished)
		var d gateRunDetail
		if err := json.Unmarshal([]byte(detail), &d); err == nil {
			r.Reason, r.Results, r.Diff, r.Warnings = d.Reason, d.Results, d.Diff, d.Warnings
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveGateApproval records (or refreshes) the DockWeave-side approval of an
// issue. There is exactly one approval per issue.
func (s *Store) SaveGateApproval(ctx context.Context, a *model.GateApproval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO gate_approvals(issue_id,actor,role,note,approved_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(issue_id) DO UPDATE SET actor=excluded.actor, role=excluded.role,
			note=excluded.note, approved_at=excluded.approved_at`,
		a.IssueID, a.Actor, a.Role, a.Note, ts(a.ApprovedAt))
	if err != nil {
		return fmt.Errorf("save gate approval: %w", err)
	}
	return nil
}

// GetGateApproval returns the approval for an issue, or ErrNotFound.
func (s *Store) GetGateApproval(ctx context.Context, issueID string) (*model.GateApproval, error) {
	a := &model.GateApproval{}
	var approved string
	err := s.db.QueryRowContext(ctx, `SELECT issue_id,actor,role,note,approved_at
		FROM gate_approvals WHERE issue_id=?`, issueID).Scan(&a.IssueID, &a.Actor, &a.Role, &a.Note, &approved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get gate approval: %w", err)
	}
	a.ApprovedAt, _ = parseTS(approved)
	return a, nil
}
