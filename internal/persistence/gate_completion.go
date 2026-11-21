package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ananta888/dockweave/internal/model"
)

// CompleteGateRun commits the result and its evidence/audit together. A failed
// commit leaves the durable incomplete run untouched, and admission blocked.
func (s *Store) CompleteGateRun(ctx context.Context, run *model.GateRun, evidence *model.Evidence, event *model.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete gate begin: %w", err)
	}
	defer tx.Rollback()
	if err := saveGateRunTx(ctx, tx, run); err != nil {
		return err
	}
	if evidence != nil {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("gate evidence: %w", err)
		}
		payload, err := json.Marshal(evidence.Payload)
		if err != nil {
			return fmt.Errorf("gate evidence payload: %w", err)
		}
		if evidence.ID == "" {
			evidence.ID = model.NewID("evd")
		}
		if evidence.CreatedAt.IsZero() {
			evidence.CreatedAt = run.FinishedAt
		}
		var key any
		if evidence.IdempotencyKey != "" {
			key = evidence.IdempotencyKey
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO evidence(id,task_id,kind,status,ref,verified,payload,idempotency_key,created_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO UPDATE SET status=excluded.status,ref=excluded.ref,
		verified=excluded.verified,payload=excluded.payload,created_at=excluded.created_at`, evidence.ID, evidence.TaskID, evidence.Kind, evidence.Status, evidence.Ref, evidence.Verified, string(payload), key, ts(evidence.CreatedAt)); err != nil {
			return fmt.Errorf("complete gate evidence: %w", err)
		}
	}
	if event != nil {
		if err := appendEventTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete gate commit: %w", err)
	}
	return nil
}

// RelevantGateRuns finds one newest relevant run with two bounded index seeks:
// the gate's verdict and the newest globally relevant result. Retention loss
// returns no evidence, while unrelated phase runs never hide retained evidence.
func (s *Store) RelevantGateRuns(ctx context.Context, workspace, gateID string) ([]*model.GateRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.workspace,r.issue_id,r.image,r.image_digest,r.status,r.gates,r.passed,
	 r.duration_ms,r.gates_hash,r.tamper,r.actor,r.detail,r.started_at,r.finished_at,r.workspace_fingerprint,r.upstream_agent_id,r.phases
	 FROM gate_runs r WHERE r.id=(SELECT run_id FROM (
	 SELECT * FROM (SELECT run_id,started_at FROM gate_run_relevance WHERE workspace=? AND gate_id=? ORDER BY started_at DESC,run_id DESC LIMIT 1)
	 UNION ALL SELECT * FROM (SELECT run_id,started_at FROM gate_run_relevance WHERE workspace=? AND gate_id='' ORDER BY started_at DESC,run_id DESC LIMIT 1)
	 ) ORDER BY started_at DESC,run_id DESC LIMIT 1)`, workspace, gateID, workspace)
	if err != nil {
		return nil, fmt.Errorf("relevant gate query: %w", err)
	}
	defer rows.Close()
	var out []*model.GateRun
	for rows.Next() {
		r := &model.GateRun{}
		var status, detail, started, finished, phases string
		var tamper int
		if err := rows.Scan(&r.ID, &r.Workspace, &r.IssueID, &r.Image, &r.ImageDigest, &status, &r.Gates, &r.Passed, &r.DurationMS, &r.GatesHash, &tamper, &r.Actor, &detail, &started, &finished, &r.WorkspaceFingerprint, &r.UpstreamAgentID, &phases); err != nil {
			return nil, fmt.Errorf("scan relevant gate: %w", err)
		}
		r.Status = model.GateRunStatus(status)
		r.Tamper = tamper == 1
		if phases != "" {
			r.Phases = strings.Split(phases, ",")
		}
		if r.StartedAt, err = parseTS(started); err != nil {
			return nil, fmt.Errorf("gate start: %w", err)
		}
		if r.FinishedAt, err = parseTS(finished); err != nil {
			return nil, fmt.Errorf("gate finish: %w", err)
		}
		var d gateRunDetail
		if err := json.Unmarshal([]byte(detail), &d); err != nil {
			return nil, fmt.Errorf("decode relevant gate: %w", err)
		}
		r.Reason, r.Results, r.Diff, r.Budget, r.Warnings = d.Reason, d.Results, d.Diff, d.Budget, d.Warnings
		out = append(out, r)
	}
	return out, rows.Err()
}
