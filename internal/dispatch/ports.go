// Package dispatch is the provider-neutral worker loop (ADR 0003 D-4): it
// takes the next ticket of an agent, enforces dependencies, pipeline
// admission and gates, drives the isolated agent, applies the STATUS and
// VERDICT contracts and routes the pipeline. It replaces the logic of
// deployments/paperclip/dockweave-run.sh and pipeline.py and depends only on
// the ports below, so it runs against any ticket provider.
package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Admitter answers pipeline admission for an agent (daemon evaluator).
type Admitter interface {
	Admit(ctx context.Context, agentID string) (*model.Admission, error)
}

// PipelineEdges lists the enforced gate -> agent edges.
type PipelineEdges interface {
	Edges(ctx context.Context) (revision int, edges []model.PipelineEdge, err error)
}

// GateRequest asks for a sandboxed gate run.
type GateRequest struct {
	Workspace       string
	IssueID         string
	UpstreamAgentID string
	Phases          []string
}

// GateRunner executes gates (daemon gate runner).
type GateRunner interface {
	Run(ctx context.Context, req GateRequest) (*model.GateRun, error)
}

// ErrPipelineBlocked is returned by a Driver when the execution endpoint
// refused the agent (race with a gate change after the admission check).
var ErrPipelineBlocked = errors.New("pipeline_blocked")

// BlockedError carries the admission of a refused drive.
type BlockedError struct{ Admission *model.Admission }

func (e *BlockedError) Error() string { return "pipeline_blocked" }
func (e *BlockedError) Unwrap() error { return ErrPipelineBlocked }

// Driver sends one prompt to an agent and returns its answer.
type Driver interface {
	Prompt(ctx context.Context, agentID, ticketRef, prompt string) (string, error)
}

// Waker wakes an agent so it looks for work now (provider wake or the
// native scheduler).
type Waker interface {
	Wake(ctx context.Context, agentID, reason string) error
}

// Reviews mirrors a reviewer verdict into DockWeave (audited evidence).
type Reviews interface {
	RecordVerdict(ctx context.Context, ticketRef, verdict, note, actor string) error
}

// ActionLog stores idempotency markers durably.
type ActionLog interface {
	GetDispatchAction(ctx context.Context, key string) (model.DispatchAction, error)
	PutDispatchAction(ctx context.Context, a model.DispatchAction) error
}

// Config tunes the dispatcher. Zero values are safe defaults.
type Config struct {
	// Workspace returns the host path gates run in for a ticket ("" = none:
	// a DONE cannot be confirmed and stays open).
	Workspace func(ctx context.Context, workspaceID string) string
	// Reviewer reports whether an agent is the reviewer (VERDICT contract).
	Reviewer func(agentID string) bool
	// BlockedStatus re-queues a blocked ticket (default todo).
	BlockedStatus string
	// ReconcileWindow bounds how long an unknown outcome blocks a retry
	// (default 600 s, like DOCKWEAVE_PIPELINE_RECONCILE_SEC).
	ReconcileWindow time.Duration
	// PromptTimeout bounds one agent turn (default 240 s).
	PromptTimeout time.Duration
	Now           func() time.Time
}
