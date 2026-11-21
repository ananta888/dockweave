package model

import "time"

// MaxPipelineAgeSec is the supported freshness horizon (ten years). Zero
// remains unlimited; the bound is checked before duration multiplication.
const MaxPipelineAgeSec = 10 * 365 * 24 * 60 * 60

// PipelineEdge is one gate -> agent precondition derived from the 2D graph:
// the downstream agent may only take pipeline work while the gate's latest
// relevant run passed on the current workspace content (ADR 0002).
type PipelineEdge struct {
	ID                string `json:"id"`
	GateID            string `json:"gate_id"`
	UpstreamAgentID   string `json:"upstream_agent_id,omitempty"`
	DownstreamAgentID string `json:"downstream_agent_id"`
	// Workspace is the canonical workspace path the gate runs in.
	Workspace string `json:"workspace"`
	// OnTrue/OnFalse are routing actions (allow|deny|wake:<agent>|rework).
	OnTrue      string `json:"on_true,omitempty"`
	OnFalse     string `json:"on_false,omitempty"`
	ReasonTrue  string `json:"reason_true,omitempty"`
	ReasonFalse string `json:"reason_false,omitempty"`
	// MaxAgeSec optionally limits how old a passing run may be (0 = no limit).
	MaxAgeSec      int       `json:"max_age_sec,omitempty"`
	SourceRevision int       `json:"source_revision"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PipelineState is the stored edge set and the graph revision it came from.
type PipelineState struct {
	Revision  int             `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
	Edges     []*PipelineEdge `json:"edges"`
}

// Admission reason codes.
const (
	AdmitOK                     = "ok"
	AdmitOverride               = "override"
	AdmitNoRun                  = "no_run"
	AdmitFailed                 = "failed"
	AdmitError                  = "error"
	AdmitStale                  = "stale"
	AdmitExpired                = "expired"
	AdmitTamper                 = "tamper"
	AdmitFingerprintUnavailable = "fingerprint_unavailable"
	AdmitUnknownGate            = "unknown_gate"
	AdmitDenied                 = "denied"
	AdmitInternal               = "internal"
)

// AdmissionEdge is the evaluation of one incoming edge.
type AdmissionEdge struct {
	EdgeID          string     `json:"edge_id"`
	GateID          string     `json:"gate_id"`
	UpstreamAgentID string     `json:"upstream_agent_id,omitempty"`
	Workspace       string     `json:"workspace"`
	Satisfied       bool       `json:"satisfied"`
	Code            string     `json:"code"`
	Message         string     `json:"message,omitempty"`
	RunID           string     `json:"run_id,omitempty"`
	RunStatus       string     `json:"run_status,omitempty"`
	RunFinishedAt   *time.Time `json:"run_finished_at,omitempty"`
	// Action is the routing action that applies to this edge's state
	// (on_true when the gate passed, on_false when it failed, deny otherwise).
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	OverrideID string `json:"override_id,omitempty"`
}

// Admission answers "may this agent take pipeline work right now?".
type Admission struct {
	AgentID string `json:"agent_id"`
	Agent   string `json:"agent,omitempty"`
	Allowed bool   `json:"allowed"`
	Action  string `json:"action"`
	// Reason is the verdict reason of the edge that determined Action
	// (reason_true/reason_false of that edge), empty when none is set.
	Reason    string          `json:"reason,omitempty"`
	Edges     []AdmissionEdge `json:"edges"`
	Revision  int             `json:"revision"`
	CheckedAt time.Time       `json:"checked_at"`
	// Fingerprints holds the current fingerprint per workspace (or empty when
	// it could not be computed).
	Fingerprints map[string]string `json:"fingerprints,omitempty"`
}

// Blocking returns the edges that are not satisfied.
func (a *Admission) Blocking() []AdmissionEdge {
	var out []AdmissionEdge
	for _, e := range a.Edges {
		if !e.Satisfied {
			out = append(out, e)
		}
	}
	return out
}

// PipelineOverride is an audited admin exception for one blocked edge.
type PipelineOverride struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agent_id"`
	GateID    string     `json:"gate_id"`
	Reason    string     `json:"reason"`
	Actor     string     `json:"actor"`
	SingleUse bool       `json:"single_use"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

// Active reports whether the override can still be used at t.
func (o *PipelineOverride) Active(t time.Time) bool {
	if o == nil || !t.Before(o.ExpiresAt) {
		return false
	}
	return !o.SingleUse || o.UsedAt == nil
}
