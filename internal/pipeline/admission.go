// Package pipeline evaluates gate -> agent pipeline edges (ADR 0002): a
// downstream agent may only take pipeline work while every incoming gate's
// latest relevant run passed on exactly the current workspace content.
//
// The evaluation is fail-closed: any internal error, missing run, missing
// fingerprint or unexpected state blocks the agent with a structured reason.
// Only an agent without incoming edges is unconditionally allowed.
package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/internal/model"
)

// Store is the persistence subset the evaluator needs.
type Store interface {
	EdgesForDownstream(ctx context.Context, agentID string) ([]*model.PipelineEdge, int, error)
	RecentGateRuns(ctx context.Context, workspace string, limit int) ([]*model.GateRun, error)
	ListPipelineOverrides(ctx context.Context, agentID string, activeAt time.Time) ([]*model.PipelineOverride, error)
	ConsumePipelineOverride(ctx context.Context, id string, at time.Time) error
}

// FingerprintFunc computes the current fingerprint of a canonical workspace.
type FingerprintFunc func(ctx context.Context, workspace string) (string, error)

// Evaluator answers admission questions.
type Evaluator struct {
	Store       Store
	Fingerprint FingerprintFunc
	// Now is the clock (tests); nil uses time.Now.
	Now func() time.Time
	// RunScan is how many recent runs per workspace are searched for the
	// gate (default 200).
	RunScan int
}

func (e *Evaluator) now() time.Time {
	if e != nil && e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Admission evaluates the agent without side effects (read-only endpoint,
// UI status). Single-use overrides are reported but not consumed.
func (e *Evaluator) Admission(ctx context.Context, agentID string) *model.Admission {
	return e.evaluate(ctx, agentID)
}

// Admit evaluates the agent for an actual unit of pipeline work and consumes
// the single-use overrides it relies on. If an override cannot be consumed
// (used concurrently, expired) the agent is blocked.
func (e *Evaluator) Admit(ctx context.Context, agentID string) *model.Admission {
	adm := e.evaluate(ctx, agentID)
	if !adm.Allowed {
		return adm
	}
	at := e.now()
	for i := range adm.Edges {
		ed := &adm.Edges[i]
		if ed.Code != model.AdmitOverride || ed.OverrideID == "" {
			continue
		}
		if err := e.Store.ConsumePipelineOverride(ctx, ed.OverrideID, at); err != nil {
			ed.Satisfied = false
			ed.Code = model.AdmitDenied
			ed.Message = "override could not be used: " + err.Error()
			ed.Action = gatesync.ActionDeny
		}
	}
	finalize(adm)
	return adm
}

func (e *Evaluator) evaluate(ctx context.Context, agentID string) *model.Admission {
	adm := &model.Admission{AgentID: agentID, CheckedAt: e.now(), Edges: []model.AdmissionEdge{}}
	if e == nil || e.Store == nil {
		adm.Edges = append(adm.Edges, internalEdge("pipeline evaluator is not configured"))
		finalize(adm)
		return adm
	}
	edges, rev, err := e.Store.EdgesForDownstream(ctx, agentID)
	adm.Revision = rev
	if err != nil {
		adm.Edges = append(adm.Edges, internalEdge("read pipeline edges: "+err.Error()))
		finalize(adm)
		return adm
	}
	if len(edges) == 0 {
		finalize(adm)
		return adm
	}

	overrides, overrideErr := e.Store.ListPipelineOverrides(ctx, agentID, adm.CheckedAt)

	fps := map[string]string{}
	fpErr := map[string]error{}
	fingerprint := func(ws string) (string, error) {
		if fp, ok := fps[ws]; ok {
			return fp, fpErr[ws]
		}
		var (
			fp  string
			err error
		)
		switch {
		case e.Fingerprint == nil:
			err = fmt.Errorf("fingerprinting is not configured")
		case !filepath.IsAbs(ws):
			err = fmt.Errorf("edge workspace %q is not an absolute path", ws)
		default:
			fp, err = e.Fingerprint(ctx, ws)
		}
		fps[ws], fpErr[ws] = fp, err
		return fp, err
	}
	runsCache := map[string][]*model.GateRun{}
	runsErr := map[string]error{}

	for _, edge := range edges {
		runs, ok := runsCache[edge.Workspace]
		if !ok {
			runs, err = e.Store.RecentGateRuns(ctx, edge.Workspace, e.runScan())
			runsCache[edge.Workspace], runsErr[edge.Workspace] = runs, err
		}
		ae := e.evalEdge(edge, runs, runsErr[edge.Workspace], fingerprint, adm.CheckedAt)
		if !ae.Satisfied && ae.Code != model.AdmitTamper {
			if overrideErr == nil {
				if o := findOverride(overrides, edge.GateID); o != nil {
					ae.Satisfied = true
					ae.Message = fmt.Sprintf("override %s by %s (%s) instead of %s: %s", o.ID, o.Actor, o.Reason, ae.Code, ae.Message)
					ae.Code = model.AdmitOverride
					ae.OverrideID = o.ID
					ae.Action = gatesync.ActionAllow
				}
			}
		}
		adm.Edges = append(adm.Edges, ae)
	}
	if len(fps) > 0 {
		adm.Fingerprints = map[string]string{}
		for ws, fp := range fps {
			adm.Fingerprints[ws] = fp
		}
	}
	finalize(adm)
	return adm
}

func (e *Evaluator) runScan() int {
	if e.RunScan > 0 {
		return e.RunScan
	}
	return 200
}

func findOverride(list []*model.PipelineOverride, gateID string) *model.PipelineOverride {
	for _, o := range list {
		if o.GateID == gateID || o.GateID == "*" {
			return o
		}
	}
	return nil
}

func internalEdge(msg string) model.AdmissionEdge {
	return model.AdmissionEdge{Code: model.AdmitInternal, Message: msg, Action: gatesync.ActionDeny}
}

// evalEdge evaluates one edge against the newest relevant run.
func (e *Evaluator) evalEdge(edge *model.PipelineEdge, runs []*model.GateRun, runsErr error,
	fingerprint func(string) (string, error), now time.Time) model.AdmissionEdge {
	tAct, fAct, tReason, fReason := gatesync.ResolveRouting(edge.OnTrue, edge.OnFalse, edge.ReasonTrue, edge.ReasonFalse)
	ae := model.AdmissionEdge{
		EdgeID: edge.ID, GateID: edge.GateID, UpstreamAgentID: edge.UpstreamAgentID,
		Workspace: edge.Workspace, Action: gatesync.ActionDeny,
	}
	block := func(code, msg string) model.AdmissionEdge {
		ae.Code, ae.Message = code, msg
		return ae
	}
	if runsErr != nil {
		return block(model.AdmitInternal, "read gate runs: "+runsErr.Error())
	}
	run := relevantRun(runs, edge.GateID)
	if run == nil {
		return block(model.AdmitNoRun, fmt.Sprintf("gate %q has no run in %s yet", edge.GateID, edge.Workspace))
	}
	ae.RunID = run.ID
	ae.RunStatus = string(run.Status)
	if !run.FinishedAt.IsZero() {
		t := run.FinishedAt
		ae.RunFinishedAt = &t
	}
	if run.Tamper {
		return block(model.AdmitTamper, "protected gate files changed during run "+run.ID)
	}
	res := resultFor(run, edge.GateID)
	if res == nil {
		if run.Status == model.GateError && len(run.Results) == 0 {
			return block(model.AdmitError, firstNonEmpty(run.Reason, "gate run failed before any gate ran"))
		}
		return block(model.AdmitUnknownGate, fmt.Sprintf("gate %q is not configured in %s (run %s)", edge.GateID, edge.Workspace, run.ID))
	}
	switch {
	case res.Status == "error" || run.Status == model.GateError:
		return block(model.AdmitError, firstNonEmpty(res.Reason, run.Reason, "gate errored"))
	case res.Status != "passed" || run.Status != model.GatePassed:
		ae.Action = fAct.String()
		ae.Reason = fReason
		return block(model.AdmitFailed, firstNonEmpty(res.Reason, run.Reason, "gate failed"))
	}
	if edge.MaxAgeSec > 0 && now.Sub(run.FinishedAt) > time.Duration(edge.MaxAgeSec)*time.Second {
		return block(model.AdmitExpired, fmt.Sprintf("passing run %s is older than %ds", run.ID, edge.MaxAgeSec))
	}
	if run.WorkspaceFingerprint == "" {
		return block(model.AdmitStale, "run "+run.ID+" has no workspace fingerprint; run the gate again")
	}
	current, err := fingerprint(edge.Workspace)
	if err != nil {
		return block(model.AdmitFingerprintUnavailable, err.Error())
	}
	if current != run.WorkspaceFingerprint {
		return block(model.AdmitStale, "workspace changed since passing run "+run.ID)
	}
	if !tAct.Allows() {
		return block(model.AdmitDenied, "on_true action "+tAct.String()+" does not release the agent")
	}
	ae.Satisfied = true
	ae.Code = model.AdmitOK
	ae.Action = tAct.String()
	ae.Reason = tReason
	return ae
}

// relevantRun is the newest run that decides about gateID: one that executed
// the gate, a tamper run, or a run over all phases (which, without the gate,
// proves the gate is not configured). Phase-limited runs without the gate are
// skipped, so a pre_run-only run never hides the last after_run verdict.
func relevantRun(runs []*model.GateRun, gateID string) *model.GateRun {
	for _, r := range runs {
		if r.Tamper || resultFor(r, gateID) != nil || len(r.Phases) == 0 {
			return r
		}
	}
	return nil
}

func resultFor(r *model.GateRun, gateID string) *model.GateResult {
	for i := range r.Results {
		if r.Results[i].ID == gateID {
			return &r.Results[i]
		}
	}
	return nil
}

// finalize derives the overall verdict and action.
func finalize(adm *model.Admission) {
	adm.Allowed = true
	for _, e := range adm.Edges {
		if !e.Satisfied {
			adm.Allowed = false
		}
	}
	if adm.Allowed {
		adm.Action = gatesync.ActionAllow
		for _, e := range adm.Edges {
			if strings.HasPrefix(e.Action, gatesync.ActionWake+":") {
				adm.Action = e.Action
				break
			}
		}
		return
	}
	adm.Action = gatesync.ActionDeny
	for _, e := range adm.Edges {
		if !e.Satisfied && e.Action == gatesync.ActionRework {
			adm.Action = gatesync.ActionRework
			break
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
