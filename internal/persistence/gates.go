package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

const gateReviewPrefix = "gate-review:"

func gateTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

type gateRunDetail struct {
	Reason   string             `json:"reason,omitempty"`
	Results  []model.GateResult `json:"results,omitempty"`
	Diff     *model.GateDiff    `json:"diff,omitempty"`
	Budget   *model.GateBudget  `json:"budget,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
}

// SaveGateRun inserts or replaces a gate run (idempotent by id).
func (s *Store) SaveGateRun(ctx context.Context, r *model.GateRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gate run begin: %w", err)
	}
	defer tx.Rollback()
	if err := saveGateRunTx(ctx, tx, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gate run commit: %w", err)
	}
	return nil
}

func saveGateRunTx(ctx context.Context, tx *sql.Tx, r *model.GateRun) error {
	detail, err := json.Marshal(gateRunDetail{
		Reason: r.Reason, Results: r.Results, Diff: r.Diff, Budget: r.Budget, Warnings: r.Warnings,
	})
	if err != nil {
		return fmt.Errorf("encode gate run: %w", err)
	}
	tamper := 0
	if r.Tamper {
		tamper = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gate_runs(
		id,workspace,issue_id,image,image_digest,status,gates,passed,duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at,
		workspace_fingerprint,upstream_agent_id,phases)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			image=excluded.image, image_digest=excluded.image_digest,
			status=excluded.status, gates=excluded.gates, passed=excluded.passed,
			duration_ms=excluded.duration_ms, gates_hash=excluded.gates_hash,
			tamper=excluded.tamper, detail=excluded.detail, finished_at=excluded.finished_at,
			workspace_fingerprint=excluded.workspace_fingerprint`,
		r.ID, r.Workspace, r.IssueID, r.Image, r.ImageDigest, string(r.Status), r.Gates, r.Passed,
		r.DurationMS, r.GatesHash, tamper, r.Actor, string(detail), gateTS(r.StartedAt), gateTS(r.FinishedAt),
		r.WorkspaceFingerprint, r.UpstreamAgentID, strings.Join(r.Phases, ","))
	if err != nil {
		return fmt.Errorf("save gate run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gate_run_relevance WHERE run_id=?`, r.ID); err != nil {
		return fmt.Errorf("clear gate relevance: %w", err)
	}
	ids := map[string]bool{}
	for _, res := range r.Results {
		ids[res.ID] = true
	}
	if r.RelevantToAllGates() {
		ids[""] = true
	}
	for id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gate_run_relevance(run_id,workspace,gate_id,started_at) VALUES(?,?,?,?)`, r.ID, r.Workspace, id, gateTS(r.StartedAt)); err != nil {
			return fmt.Errorf("save gate relevance: %w", err)
		}
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
		rows, err = s.db.QueryContext(ctx, `SELECT id,workspace,issue_id,image,image_digest,status,gates,passed,
			duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at,
			workspace_fingerprint,upstream_agent_id,phases
			FROM gate_runs ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id,workspace,issue_id,image,image_digest,status,gates,passed,
			duration_ms,gates_hash,tamper,actor,detail,started_at,finished_at,
			workspace_fingerprint,upstream_agent_id,phases
			FROM gate_runs WHERE workspace=? ORDER BY started_at DESC, id DESC LIMIT ?`, workspace, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list gate runs: %w", err)
	}
	defer rows.Close()
	out := make([]*model.GateRun, 0, limit)
	for rows.Next() {
		r := &model.GateRun{}
		var status, detail, started, finished, phases string
		var tamper int
		if err := rows.Scan(&r.ID, &r.Workspace, &r.IssueID, &r.Image, &r.ImageDigest, &status, &r.Gates, &r.Passed,
			&r.DurationMS, &r.GatesHash, &tamper, &r.Actor, &detail, &started, &finished,
			&r.WorkspaceFingerprint, &r.UpstreamAgentID, &phases); err != nil {
			return nil, fmt.Errorf("scan gate run: %w", err)
		}
		if phases != "" {
			r.Phases = strings.Split(phases, ",")
		}
		r.Status = model.GateRunStatus(status)
		r.Tamper = tamper == 1
		r.StartedAt, _ = parseTS(started)
		r.FinishedAt, _ = parseTS(finished)
		var d gateRunDetail
		if err := json.Unmarshal([]byte(detail), &d); err == nil {
			r.Reason, r.Results, r.Diff, r.Budget, r.Warnings = d.Reason, d.Results, d.Diff, d.Budget, d.Warnings
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

// UpsertGateReview records the review verdict for an issue exactly once. A
// second call with the same verdict is idempotent; a different verdict returns
// ErrConflict and the stored verdict so callers can surface the contradiction.
func (s *Store) UpsertGateReview(ctx context.Context, review *model.GateReview) (*model.GateReview, error) {
	key := gateReviewPrefix + review.IssueID
	var id, status, payload, created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,status,payload,created_at FROM evidence WHERE idempotency_key=?`, key).
		Scan(&id, &status, &payload, &created)
	if err == nil {
		existing := &model.GateReview{EvidenceID: id}
		existing.IssueID = review.IssueID
		existing.Verdict = "FAIL"
		if status == string(model.EvidencePassed) {
			existing.Verdict = "PASS"
		}
		var body map[string]string
		_ = json.Unmarshal([]byte(payload), &body)
		existing.Actor = body["actor"]
		existing.Note = body["note"]
		existing.AssignedReviewer = body["assigned_reviewer"]
		existing.RecordedAt, _ = parseTS(created)
		if !strings.EqualFold(existing.Verdict, review.Verdict) {
			existing.Conflict = true
			existing.Existing = existing.Verdict
			return existing, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup gate review: %w", err)
	}
	status = string(model.EvidenceFailed)
	verified := false
	if strings.EqualFold(review.Verdict, "PASS") {
		status = string(model.EvidencePassed)
		verified = true
	}
	ev := &model.Evidence{
		TaskID:         "",
		Kind:           model.EvidenceReview,
		Status:         model.EvidenceStatus(status),
		Ref:            review.IssueID,
		Verified:       verified,
		IdempotencyKey: key,
		Payload: map[string]string{
			"verdict":           strings.ToUpper(review.Verdict),
			"actor":             review.Actor,
			"note":              review.Note,
			"assigned_reviewer": review.AssignedReviewer,
			"source":            "paperclip",
		},
	}
	inserted, err := s.AddEvidence(ctx, ev)
	if err != nil {
		return nil, err
	}
	if !inserted {
		// A concurrent writer won the race; re-read the stored verdict.
		return s.UpsertGateReview(ctx, review)
	}
	review.EvidenceID = ev.ID
	review.RecordedAt = ev.CreatedAt
	review.Verdict = strings.ToUpper(review.Verdict)
	return review, nil
}

// GetGateReview returns the stored review for an issue, or ErrNotFound.
func (s *Store) GetGateReview(ctx context.Context, issueID string) (*model.GateReview, error) {
	var id, status, payload, created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,status,payload,created_at FROM evidence WHERE idempotency_key=?`,
		gateReviewPrefix+issueID).Scan(&id, &status, &payload, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get gate review: %w", err)
	}
	review, err := decodeGateReview(issueID, id, status, payload, created)
	if err != nil {
		return nil, err
	}
	return review, nil
}

// ListGateReviews returns the newest review records.
func (s *Store) ListGateReviews(ctx context.Context, limit int) ([]*model.GateReview, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,status,payload,created_at,ref FROM evidence
		WHERE idempotency_key LIKE ? ORDER BY created_at DESC LIMIT ?`, gateReviewPrefix+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("list gate reviews: %w", err)
	}
	defer rows.Close()
	var out []*model.GateReview
	for rows.Next() {
		var id, status, payload, created, ref string
		if err := rows.Scan(&id, &status, &payload, &created, &ref); err != nil {
			return nil, fmt.Errorf("scan gate review: %w", err)
		}
		r, err := decodeGateReview(ref, id, status, payload, created)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func decodeGateReview(issueID, id, status, payload, created string) (*model.GateReview, error) {
	r := &model.GateReview{IssueID: issueID, EvidenceID: id, Verdict: "FAIL"}
	if status == string(model.EvidencePassed) {
		r.Verdict = "PASS"
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(payload), &body)
	r.Actor = body["actor"]
	r.Note = body["note"]
	r.AssignedReviewer = body["assigned_reviewer"]
	r.RecordedAt, _ = parseTS(created)
	return r, nil
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

// PruneGateRuns deletes gate runs older than olderThan while always keeping the
// newest keepPerWorkspace runs per workspace (ranked over all runs of the
// workspace, not only the old ones). It returns the number of deleted
// rows. This bounds the growth of the gate_runs table (retention policy).
func (s *Store) PruneGateRuns(ctx context.Context, olderThan time.Time, keepPerWorkspace int) (int64, error) {
	if keepPerWorkspace <= 0 {
		keepPerWorkspace = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("prune gate runs begin: %w", err)
	}
	defer tx.Rollback()
	const candidates = `
			SELECT id, workspace, started_at FROM (
				SELECT id, workspace, started_at,
					ROW_NUMBER() OVER (PARTITION BY workspace ORDER BY started_at DESC, id DESC) AS rn
				FROM gate_runs
			) WHERE rn > ? AND started_at < ?`
	// Remember per workspace up to when evidence was deleted, so a missing
	// run can be reported as retention loss instead of "never ran" (RF-015).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meta(key, value)
		SELECT ?||workspace, max(started_at) FROM (`+candidates+`) WHERE true GROUP BY workspace
		ON CONFLICT(key) DO UPDATE SET value = max(value, excluded.value)`,
		gateHistoryPrunedKey, keepPerWorkspace, gateTS(olderThan)); err != nil {
		return 0, fmt.Errorf("record gate retention: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM gate_runs WHERE id IN (SELECT id FROM (`+candidates+`))`,
		keepPerWorkspace, gateTS(olderThan))
	if err != nil {
		return 0, fmt.Errorf("prune gate runs: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("prune gate runs commit: %w", err)
	}
	return n, nil
}

const gateHistoryPrunedKey = "gate_history_pruned:"

// GateHistoryPrunedAt reports the start time of the newest gate run that
// retention deleted for the workspace (ok=false if none was ever deleted).
func (s *Store) GateHistoryPrunedAt(ctx context.Context, workspace string) (time.Time, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, gateHistoryPrunedKey+workspace).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read gate retention: %w", err)
	}
	t, err := parseTS(v)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse gate retention: %w", err)
	}
	return t, true, nil
}
