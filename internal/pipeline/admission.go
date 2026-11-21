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
	"sort"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/internal/model"
)

// Store is the persistence subset the evaluator needs.
type Store interface {
	EdgesForDownstream(ctx context.Context, agentID string) ([]*model.PipelineEdge, int, error)
	RelevantGateRuns(ctx context.Context, workspace, gateID string) ([]*model.GateRun, error)
	ListPipelineOverrides(ctx context.Context, agentID string, activeAt time.Time) ([]*model.PipelineOverride, error)
	ConsumePipelineOverridesAudited(ctx context.Context, ids []string, at time.Time, actor, issue string) error
}

// retentionStore is optionally implemented by stores that record retention.
type retentionStore interface {
	GateHistoryPrunedAt(ctx context.Context, workspace string) (time.Time, bool, error)
}

// prunedAt is best effort: it only changes the message of a block that
// happens anyway, so a lookup error is ignored.
func (e *Evaluator) prunedAt(workspace string) (time.Time, bool) {
	rs, ok := e.Store.(retentionStore)
	if !ok {
		return time.Time{}, false
	}
	at, ok, err := rs.GateHistoryPrunedAt(context.Background(), workspace)
	return at, ok && err == nil
}

// FingerprintFunc computes the current fingerprint of a canonical workspace.
type FingerprintFunc func(ctx context.Context, workspace string) (string, error)

// Evaluator answers admission questions.
type Evaluator struct {
	Store       Store
	Fingerprint FingerprintFunc
	// Now is the clock (tests); nil uses time.Now.
	Now func() time.Time
	// WorkActor and WorkIssue identify the real admission in atomic audit events.
	WorkActor string
	WorkIssue string
	// RunScan is retained for source compatibility; indexed relevant-run
	// lookup no longer truncates evidence by an arbitrary history window.
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
	var ids []string
	seen := map[string]bool{}
	for _, ed := range adm.Edges {
		if ed.Code == model.AdmitOverride && ed.OverrideID != "" && !seen[ed.OverrideID] {
			ids = append(ids, ed.OverrideID)
			seen[ed.OverrideID] = true
		}
	}
	if len(ids) == 0 {
		return adm
	}
	if err := e.Store.ConsumePipelineOverridesAudited(ctx, ids, at, e.WorkActor, e.WorkIssue); err != nil {
		for i := range adm.Edges {
			ed := &adm.Edges[i]
			if ed.Code != model.AdmitOverride || ed.OverrideID == "" {
				continue
			}
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
	if err := ctx.Err(); err != nil {
		adm.Edges = append(adm.Edges, internalEdge(err.Error()))
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
		cacheKey := edge.Workspace + "\x00" + edge.GateID
		runs, ok := runsCache[cacheKey]
		if !ok {
			runs, err = e.Store.RelevantGateRuns(ctx, edge.Workspace, edge.GateID)
			runsCache[cacheKey], runsErr[cacheKey] = runs, err
		}
		ae := e.evalEdge(edge, runs, runsErr[cacheKey], fingerprint, adm.CheckedAt)
		// Overrides bridge missing, failed or stale evidence only. Tamper and
		// a deliberate deny/rework after a fresh PASS (denied) stay blocked.
		if !ae.Satisfied && ae.Code != model.AdmitTamper && ae.Code != model.AdmitDenied {
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

// Admissions evaluates one immutable edge snapshot with request-local
// fingerprint caching. Nothing is shared with subsequent real admissions.
func (e *Evaluator) Admissions(ctx context.Context, state *model.PipelineState) []*model.Admission {
	copy := *e
	copy.Store = &snapshotStore{Store: e.Store, state: state, runs: map[string]cachedRuns{}}
	at := e.now()
	copy.Now = func() time.Time { return at }
	type result struct {
		fp  string
		err error
	}
	cache := map[string]result{}
	copy.Fingerprint = func(ctx context.Context, ws string) (string, error) {
		if r, ok := cache[ws]; ok {
			return r.fp, r.err
		}
		if e.Fingerprint == nil {
			return "", fmt.Errorf("fingerprinting is not configured")
		}
		fp, err := e.Fingerprint(ctx, ws)
		cache[ws] = result{fp, err}
		return fp, err
	}
	ids := map[string]bool{}
	for _, edge := range state.Edges {
		ids[edge.DownstreamAgentID] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	out := make([]*model.Admission, 0, len(ordered))
	for _, id := range ordered {
		out = append(out, copy.Admission(ctx, id))
	}
	return out
}

type snapshotStore struct {
	Store
	state *model.PipelineState
	runs  map[string]cachedRuns
}

type cachedRuns struct {
	runs []*model.GateRun
	err  error
}

func (s *snapshotStore) RelevantGateRuns(ctx context.Context, workspace, gateID string) ([]*model.GateRun, error) {
	key := workspace + "\x00" + gateID
	if r, ok := s.runs[key]; ok {
		return r.runs, r.err
	}
	runs, err := s.Store.RelevantGateRuns(ctx, workspace, gateID)
	s.runs[key] = cachedRuns{runs, err}
	return runs, err
}

func (s *snapshotStore) EdgesForDownstream(_ context.Context, id string) ([]*model.PipelineEdge, int, error) {
	var out []*model.PipelineEdge
	for _, e := range s.state.Edges {
		if e.DownstreamAgentID == id {
			out = append(out, e)
		}
	}
	return out, s.state.Revision, nil
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
		msg := fmt.Sprintf("gate %q has no run in %s yet", edge.GateID, edge.Workspace)
		if at, ok := e.prunedAt(edge.Workspace); ok {
			msg = fmt.Sprintf("gate %q has no run in %s; retention removed runs of this workspace up to %s, so earlier evidence may be gone; run the gate again",
				edge.GateID, edge.Workspace, at.Format(time.RFC3339))
		}
		return block(model.AdmitNoRun, msg)
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
	if run.FinishedAt.IsZero() {
		return block(model.AdmitError, "gate run has not durably completed")
	}
	res := resultFor(run, edge.GateID)
	if res == nil {
		if run.Status == model.GateError || run.Status == model.GateFailed {
			return block(model.AdmitError, firstNonEmpty(run.Reason, "gate run failed outside this gate (setup or workspace policy)"))
		}
		return block(model.AdmitUnknownGate, fmt.Sprintf("gate %q is not configured in %s (run %s)", edge.GateID, edge.Workspace, run.ID))
	}
	if run.FinishedAt.After(now.Add(time.Minute)) || (!run.StartedAt.IsZero() && run.FinishedAt.Before(run.StartedAt)) {
		return block(model.AdmitError, "gate completion time is invalid")
	}
	if edge.MaxAgeSec < 0 || edge.MaxAgeSec > model.MaxPipelineAgeSec {
		return block(model.AdmitError, "max_age_sec is out of range")
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
	ae.Action, ae.Reason = tAct.String(), tReason
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
		if resultFor(r, gateID) != nil || r.RelevantToAllGates() {
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
