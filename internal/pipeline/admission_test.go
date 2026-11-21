package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

type fakeStore struct {
	edges     []*model.PipelineEdge
	edgesErr  error
	runs      map[string][]*model.GateRun
	runsErr   error
	overrides []*model.PipelineOverride
	consumed  map[string]bool
}

func (f *fakeStore) EdgesForDownstream(_ context.Context, agent string) ([]*model.PipelineEdge, int, error) {
	if f.edgesErr != nil {
		return nil, 0, f.edgesErr
	}
	var out []*model.PipelineEdge
	for _, e := range f.edges {
		if e.DownstreamAgentID == agent {
			out = append(out, e)
		}
	}
	return out, 7, nil
}

func (f *fakeStore) RecentGateRuns(_ context.Context, ws string, _ int) ([]*model.GateRun, error) {
	return f.runs[ws], f.runsErr
}

func (f *fakeStore) RelevantGateRuns(_ context.Context, ws, _ string) ([]*model.GateRun, error) {
	return f.runs[ws], f.runsErr
}

func (f *fakeStore) ConsumePipelineOverridesAudited(ctx context.Context, ids []string, at time.Time, _, _ string) error {
	for _, id := range ids {
		if f.consumed[id] {
			return errors.New("already used")
		}
	}
	for _, id := range ids {
		if err := f.ConsumePipelineOverride(ctx, id, at); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) ListPipelineOverrides(_ context.Context, agent string, at time.Time) ([]*model.PipelineOverride, error) {
	var out []*model.PipelineOverride
	for _, o := range f.overrides {
		if o.AgentID == agent && o.Active(at) && !f.consumed[o.ID] {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeStore) ConsumePipelineOverride(_ context.Context, id string, _ time.Time) error {
	if f.consumed == nil {
		f.consumed = map[string]bool{}
	}
	if f.consumed[id] {
		return errors.New("already used")
	}
	f.consumed[id] = true
	return nil
}

var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const ws = "/srv/ws"

func passRun(id string, fp string, age time.Duration) *model.GateRun {
	return &model.GateRun{ID: id, Workspace: ws, Status: model.GatePassed, WorkspaceFingerprint: fp,
		FinishedAt: now.Add(-age), Results: []model.GateResult{{ID: "tests", Status: "passed"}}}
}

func baseEdge() *model.PipelineEdge {
	return &model.PipelineEdge{ID: "e1", GateID: "tests", UpstreamAgentID: "a", DownstreamAgentID: "b",
		Workspace: ws, OnTrue: "allow", OnFalse: "rework", ReasonFalse: "fix it"}
}

func eval(st *fakeStore, fp string, fpErr error) *Evaluator {
	return &Evaluator{Store: st, Now: func() time.Time { return now },
		Fingerprint: func(context.Context, string) (string, error) { return fp, fpErr }}
}

func TestAdmissionReasons(t *testing.T) {
	failed := passRun("r-f", "fp1", time.Minute)
	failed.Status = model.GateFailed
	failed.Results[0].Status = "failed"
	failed.Results[0].Reason = "tests red"
	errored := passRun("r-e", "fp1", time.Minute)
	errored.Status = model.GateError
	errored.Results[0].Status = "error"
	setupErr := &model.GateRun{ID: "r-s", Workspace: ws, Status: model.GateError, Reason: "image unavailable", FinishedAt: now}
	tamper := passRun("r-t", "fp1", time.Minute)
	tamper.Tamper = true
	tamper.Status = model.GateFailed
	other := &model.GateRun{ID: "r-o", Workspace: ws, Status: model.GatePassed, FinishedAt: now,
		Results: []model.GateResult{{ID: "lint", Status: "passed"}}, WorkspaceFingerprint: "fp1"}
	policyFail := passRun("r-p", "fp1", time.Minute)
	policyFail.Status = model.GateFailed
	policyFail.Reason = "changes outside the allowed paths: x"
	noFP := passRun("r-n", "", time.Minute)
	preOnly := &model.GateRun{ID: "r-pre", Workspace: ws, Status: model.GatePassed, Phases: []string{"pre_run"},
		Results: []model.GateResult{{ID: "pre", Status: "passed"}}, FinishedAt: now}

	cases := []struct {
		name    string
		runs    []*model.GateRun
		mutate  func(*model.PipelineEdge)
		fp      string
		fpErr   error
		runsErr error
		want    string
		allowed bool
		action  string
		runID   string
	}{
		{"ok", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, nil, "fp1", nil, nil, model.AdmitOK, true, "allow", "r1"},
		{"no run", nil, nil, "fp1", nil, nil, model.AdmitNoRun, false, "deny", ""},
		{"failed", []*model.GateRun{failed}, nil, "fp1", nil, nil, model.AdmitFailed, false, "rework", "r-f"},
		{"error", []*model.GateRun{errored}, nil, "fp1", nil, nil, model.AdmitError, false, "deny", "r-e"},
		{"setup error", []*model.GateRun{setupErr, passRun("r1", "fp1", time.Minute)}, nil, "fp1", nil, nil, model.AdmitError, false, "deny", "r-s"},
		{"stale", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, nil, "fp2", nil, nil, model.AdmitStale, false, "deny", "r1"},
		{"no fingerprint in run", []*model.GateRun{noFP}, nil, "fp1", nil, nil, model.AdmitStale, false, "deny", "r-n"},
		{"expired", []*model.GateRun{passRun("r1", "fp1", time.Hour)}, func(e *model.PipelineEdge) { e.MaxAgeSec = 60 }, "fp1", nil, nil, model.AdmitExpired, false, "deny", "r1"},
		{"tamper", []*model.GateRun{tamper}, nil, "fp1", nil, nil, model.AdmitTamper, false, "deny", "r-t"},
		{"fingerprint unavailable", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, nil, "", errors.New("too big"), nil, model.AdmitFingerprintUnavailable, false, "deny", "r1"},
		{"unknown gate", []*model.GateRun{other}, nil, "fp1", nil, nil, model.AdmitUnknownGate, false, "deny", "r-o"},
		{"policy failure fails the edge", []*model.GateRun{policyFail}, nil, "fp1", nil, nil, model.AdmitFailed, false, "rework", "r-p"},
		{"phase-limited run is skipped", []*model.GateRun{preOnly, passRun("r1", "fp1", time.Minute)}, nil, "fp1", nil, nil, model.AdmitOK, true, "allow", "r1"},
		{"newest run decides", []*model.GateRun{failed, passRun("r1", "fp1", time.Minute)}, nil, "fp1", nil, nil, model.AdmitFailed, false, "rework", "r-f"},
		{"on_true deny", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, func(e *model.PipelineEdge) { e.OnTrue = "deny" }, "fp1", nil, nil, model.AdmitDenied, false, "deny", "r1"},
		{"wake action", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, func(e *model.PipelineEdge) { e.OnTrue = "wake:b" }, "fp1", nil, nil, model.AdmitOK, true, "wake:b", "r1"},
		{"relative workspace", []*model.GateRun{passRun("r1", "fp1", time.Minute)}, func(e *model.PipelineEdge) { e.Workspace = "rel" }, "fp1", nil, nil, model.AdmitNoRun, false, "deny", ""},
		{"runs error", nil, nil, "fp1", nil, errors.New("db down"), model.AdmitInternal, false, "deny", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := baseEdge()
			if c.mutate != nil {
				c.mutate(e)
			}
			st := &fakeStore{edges: []*model.PipelineEdge{e}, runs: map[string][]*model.GateRun{ws: c.runs}, runsErr: c.runsErr}
			adm := eval(st, c.fp, c.fpErr).Admission(context.Background(), "b")
			if len(adm.Edges) != 1 {
				t.Fatalf("edges = %+v", adm.Edges)
			}
			got := adm.Edges[0]
			if got.Code != c.want || adm.Allowed != c.allowed || adm.Action != c.action || got.RunID != c.runID {
				t.Fatalf("got code=%s allowed=%v action=%s run=%s (%s); want %s %v %s %s",
					got.Code, adm.Allowed, adm.Action, got.RunID, got.Message, c.want, c.allowed, c.action, c.runID)
			}
			if !got.Satisfied && got.Message == "" {
				t.Fatal("blocking edge without message")
			}
			if c.want == model.AdmitFailed && got.Reason != "fix it" {
				t.Fatalf("failed edge reason = %q", got.Reason)
			}
			if adm.Revision != 7 {
				t.Fatalf("revision = %d", adm.Revision)
			}
		})
	}
}

func TestAdmissionNoEdgesAllowed(t *testing.T) {
	adm := eval(&fakeStore{}, "", nil).Admission(context.Background(), "solo")
	if !adm.Allowed || adm.Action != "allow" || len(adm.Edges) != 0 {
		t.Fatalf("adm = %+v", adm)
	}
}

func TestAdmissionFailClosedOnStoreError(t *testing.T) {
	adm := eval(&fakeStore{edgesErr: errors.New("db locked")}, "", nil).Admission(context.Background(), "b")
	if adm.Allowed || len(adm.Edges) != 1 || adm.Edges[0].Code != model.AdmitInternal {
		t.Fatalf("adm = %+v", adm)
	}
	var nilEval *Evaluator
	if adm := nilEval.Admission(context.Background(), "b"); adm.Allowed {
		t.Fatal("nil evaluator must block")
	}
	if adm := (&Evaluator{Store: &fakeStore{edges: []*model.PipelineEdge{baseEdge()},
		runs: map[string][]*model.GateRun{ws: {passRun("r1", "fp1", 0)}}}}).Admission(context.Background(), "b"); adm.Allowed {
		t.Fatal("missing fingerprint func must block")
	}
}

func TestAdmissionMultipleEdgesAllMustPass(t *testing.T) {
	e1 := baseEdge()
	e2 := baseEdge()
	e2.ID, e2.GateID = "e2", "lint"
	run := passRun("r1", "fp1", time.Minute)
	st := &fakeStore{edges: []*model.PipelineEdge{e1, e2}, runs: map[string][]*model.GateRun{ws: {run}}}
	adm := eval(st, "fp1", nil).Admission(context.Background(), "b")
	if adm.Allowed || len(adm.Blocking()) != 1 || adm.Blocking()[0].GateID != "lint" {
		t.Fatalf("adm = %+v", adm)
	}
	run.Results = append(run.Results, model.GateResult{ID: "lint", Status: "passed"})
	adm = eval(st, "fp1", nil).Admission(context.Background(), "b")
	if !adm.Allowed {
		t.Fatalf("both gates passed: %+v", adm.Blocking())
	}
	// Edges for other agents do not affect this one.
	if adm := eval(st, "fp1", nil).Admission(context.Background(), "a"); !adm.Allowed || len(adm.Edges) != 0 {
		t.Fatalf("upstream agent affected: %+v", adm)
	}
}

func TestAdmissionFingerprintComputedOncePerWorkspace(t *testing.T) {
	e1 := baseEdge()
	e2 := baseEdge()
	e2.ID, e2.GateID = "e2", "lint"
	run := passRun("r1", "fp1", time.Minute)
	run.Results = append(run.Results, model.GateResult{ID: "lint", Status: "passed"})
	calls := 0
	ev := &Evaluator{Store: &fakeStore{edges: []*model.PipelineEdge{e1, e2}, runs: map[string][]*model.GateRun{ws: {run}}},
		Now:         func() time.Time { return now },
		Fingerprint: func(context.Context, string) (string, error) { calls++; return "fp1", nil }}
	if adm := ev.Admission(context.Background(), "b"); !adm.Allowed || calls != 1 || adm.Fingerprints[ws] != "fp1" {
		t.Fatalf("allowed=%v calls=%d fps=%v", adm.Allowed, calls, adm.Fingerprints)
	}
}

func TestAdmissionOverride(t *testing.T) {
	failed := passRun("r-f", "fp1", time.Minute)
	failed.Status = model.GateFailed
	failed.Results[0].Status = "failed"
	single := &model.PipelineOverride{ID: "o1", AgentID: "b", GateID: "tests", Reason: "hotfix", Actor: "root",
		SingleUse: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	st := &fakeStore{edges: []*model.PipelineEdge{baseEdge()}, runs: map[string][]*model.GateRun{ws: {failed}},
		overrides: []*model.PipelineOverride{single}}
	ev := eval(st, "fp1", nil)

	// Read-only admission reports the override without consuming it.
	for i := 0; i < 2; i++ {
		adm := ev.Admission(context.Background(), "b")
		if !adm.Allowed || adm.Edges[0].Code != model.AdmitOverride || adm.Edges[0].OverrideID != "o1" {
			t.Fatalf("admission %d = %+v", i, adm.Edges)
		}
	}
	if len(st.consumed) != 0 {
		t.Fatal("Admission must not consume overrides")
	}
	// Admit consumes it exactly once.
	if adm := ev.Admit(context.Background(), "b"); !adm.Allowed {
		t.Fatalf("first admit blocked: %+v", adm.Edges)
	}
	if adm := ev.Admit(context.Background(), "b"); adm.Allowed || adm.Edges[0].Code != model.AdmitFailed {
		t.Fatalf("second admit must be blocked again: %+v", adm.Edges)
	}

	// Expired overrides do nothing.
	st.overrides = []*model.PipelineOverride{{ID: "o2", AgentID: "b", GateID: "tests", SingleUse: false,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}}
	if adm := ev.Admit(context.Background(), "b"); adm.Allowed {
		t.Fatal("expired override used")
	}
}

func TestAdmissionOverrideNeverForTamper(t *testing.T) {
	tamper := passRun("r-t", "fp1", time.Minute)
	tamper.Tamper = true
	st := &fakeStore{edges: []*model.PipelineEdge{baseEdge()}, runs: map[string][]*model.GateRun{ws: {tamper}},
		overrides: []*model.PipelineOverride{{ID: "o1", AgentID: "b", GateID: "*", SingleUse: false,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}}
	adm := eval(st, "fp1", nil).Admit(context.Background(), "b")
	if adm.Allowed || adm.Edges[0].Code != model.AdmitTamper {
		t.Fatalf("tamper overridden: %+v", adm.Edges)
	}
}

func TestAdmitBlocksWhenOverrideConsumptionFails(t *testing.T) {
	st := &fakeStore{edges: []*model.PipelineEdge{baseEdge()}, runs: map[string][]*model.GateRun{},
		overrides: []*model.PipelineOverride{{ID: "o1", AgentID: "b", GateID: "tests", SingleUse: true,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}}
	ev := eval(st, "fp1", nil)
	adm := ev.Admission(context.Background(), "b")
	if !adm.Allowed {
		t.Fatalf("override should allow: %+v", adm.Edges)
	}
	// A concurrent consumer wins between Admission and Admit: the batch
	// consumption fails, so the work stays blocked and nothing is marked used.
	ev.Store = &consumeFails{st}
	if adm := ev.Admit(context.Background(), "b"); adm.Allowed || adm.Edges[0].Code != model.AdmitDenied {
		t.Fatalf("adm = %+v", adm.Edges)
	}
	if st.consumed["o1"] {
		t.Fatal("failed consumption marked the override as used")
	}
}

type consumeFails struct{ *fakeStore }

func (c *consumeFails) ConsumePipelineOverridesAudited(context.Context, []string, time.Time, string, string) error {
	return errors.New("conflict: already used")
}
