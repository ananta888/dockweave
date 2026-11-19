package model

import "time"

// GateRunStatus is the outcome of one sandboxed gate run.
type GateRunStatus string

const (
	GatePassed  GateRunStatus = "passed"
	GateFailed  GateRunStatus = "failed"
	GateError   GateRunStatus = "error"
	GateSkipped GateRunStatus = "skipped"
)

// GateResult is one command inside a gate run.
type GateResult struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd,omitempty"`
	Status     string `json:"status"` // passed|failed|error
	Exit       int    `json:"exit"`
	Output     string `json:"output,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"`
}

// GateDiff summarises workspace changes observed after the run.
type GateDiff struct {
	Changed         []string `json:"changed,omitempty"`
	OutOfScope      []string `json:"out_of_scope,omitempty"`
	NewDependencies []string `json:"new_dependencies,omitempty"`
	Secrets         []string `json:"secrets,omitempty"`
}

// GateBudget is the broker usage snapshot used by the budget gate.
type GateBudget struct {
	GrantID        string  `json:"grant_id"`
	RequestsDay    int     `json:"requests_day"`
	TokensDay      int64   `json:"tokens_day"`
	CostDayUSD     float64 `json:"cost_day_usd"`
	MaxRequestsDay int     `json:"max_requests_day,omitempty"`
	MaxTokensDay   int64   `json:"max_tokens_day,omitempty"`
	MaxCostDayUSD  float64 `json:"max_cost_day_usd,omitempty"`
	Exceeded       bool    `json:"exceeded"`
	Reason         string  `json:"reason,omitempty"`
}

// GateReview is the DockWeave-side record of a Paperclip review verdict.
type GateReview struct {
	IssueID    string    `json:"issue_id"`
	Verdict    string    `json:"verdict"` // PASS|FAIL
	Actor      string    `json:"actor,omitempty"`
	Note       string    `json:"note,omitempty"`
	EvidenceID string    `json:"evidence_id,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	Conflict   bool      `json:"conflict,omitempty"`
	Existing   string    `json:"existing,omitempty"`
}

// GateRun records one sandboxed execution of a workspace gate config.
type GateRun struct {
	ID          string        `json:"id"`
	Workspace   string        `json:"workspace"`
	IssueID     string        `json:"issue_id,omitempty"`
	Image       string        `json:"image"`
	ImageDigest string        `json:"image_digest,omitempty"`
	Status      GateRunStatus `json:"status"`
	Gates       int           `json:"gates"`
	Passed      int           `json:"passed"`
	DurationMS  int64         `json:"duration_ms"`
	GatesHash   string        `json:"gates_hash,omitempty"`
	Tamper      bool          `json:"tamper"`
	Actor       string        `json:"actor,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Results     []GateResult  `json:"results,omitempty"`
	Diff        *GateDiff     `json:"diff,omitempty"`
	Budget      *GateBudget   `json:"budget,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	FinishedAt  time.Time     `json:"finished_at"`
}

// GateApproval is a DockWeave-side human approval for a Paperclip issue. It is
// bound to the authenticated peer role (admin) and audited; Paperclip's
// interaction is only the transport.
type GateApproval struct {
	IssueID    string    `json:"issue_id"`
	Actor      string    `json:"actor"`
	Role       string    `json:"role"`
	Note       string    `json:"note,omitempty"`
	ApprovedAt time.Time `json:"approved_at"`
}
