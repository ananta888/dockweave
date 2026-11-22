package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/memory"
)

// ---- fakes ----

type fakeAdmit struct {
	mu   sync.Mutex
	m    map[string]*model.Admission
	errs map[string]error
}

func (f *fakeAdmit) set(agent string, a *model.Admission) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a != nil && a.AgentID == "" {
		a.AgentID = agent
	}
	f.m[agent] = a
}

func (f *fakeAdmit) Admit(_ context.Context, agent string) (*model.Admission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[agent]; err != nil {
		return nil, err
	}
	if a, ok := f.m[agent]; ok {
		c := *a
		c.Edges = append([]model.AdmissionEdge(nil), a.Edges...)
		return &c, nil
	}
	return &model.Admission{AgentID: agent, Agent: agent, Allowed: true, Action: "allow"}, nil
}

type fakeEdges struct {
	rev   int
	edges []model.PipelineEdge
	err   error
}

func (f *fakeEdges) Edges(context.Context) (int, []model.PipelineEdge, error) {
	return f.rev, f.edges, f.err
}

type fakeGates struct {
	mu    sync.Mutex
	calls []GateRequest
	byPh  map[string]*model.GateRun
	err   error
}

func (f *fakeGates) Run(_ context.Context, r GateRequest) (*model.GateRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	if f.err != nil {
		return nil, f.err
	}
	if run, ok := f.byPh[strings.Join(r.Phases, ",")]; ok {
		return run, nil
	}
	return &model.GateRun{ID: "run-skip", Status: model.GateSkipped}, nil
}

type fakeDriver struct {
	mu      sync.Mutex
	prompts []string
	answer  string
	err     error
}

func (f *fakeDriver) Prompt(_ context.Context, _, _, p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, p)
	return f.answer, f.err
}

type fakeWaker struct {
	mu    sync.Mutex
	woken []string
	err   error
}

func (f *fakeWaker) Wake(_ context.Context, agent, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.woken = append(f.woken, agent)
	return nil
}

type memLog struct {
	mu sync.Mutex
	m  map[string]model.DispatchAction
}

func (l *memLog) GetDispatchAction(_ context.Context, k string) (model.DispatchAction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.m[k]
	if !ok {
		return model.DispatchAction{Key: k}, nil
	}
	return a, nil
}

func (l *memLog) PutDispatchAction(_ context.Context, a model.DispatchAction) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[a.Key] = a
	return nil
}

type fakeReviews struct{ verdicts []string }

func (f *fakeReviews) RecordVerdict(_ context.Context, ref, verdict, _, _ string) error {
	f.verdicts = append(f.verdicts, ref+"="+verdict)
	return nil
}

type rig struct {
	d      *Dispatcher
	p      ticketing.Provider
	admit  *fakeAdmit
	edges  *fakeEdges
	gates  *fakeGates
	driver *fakeDriver
	waker  *fakeWaker
	log    *memLog
	rev    *fakeReviews
	clock  time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{p: memory.New("memory"), admit: &fakeAdmit{m: map[string]*model.Admission{}, errs: map[string]error{}},
		edges: &fakeEdges{rev: 1}, gates: &fakeGates{byPh: map[string]*model.GateRun{}}, driver: &fakeDriver{answer: "done\nSTATUS: DONE"},
		waker: &fakeWaker{}, log: &memLog{m: map[string]model.DispatchAction{}}, rev: &fakeReviews{},
		clock: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	r.d = &Dispatcher{Tickets: r.p, Admit: r.admit, Pipeline: r.edges, Gates: r.gates, Driver: r.driver, Waker: r.waker,
		Reviews: r.rev, Log: r.log, Config: Config{
			Workspace: func(context.Context, string) string { return "/ws" },
			Reviewer:  func(a string) bool { return a == "reviewer" },
			Now:       func() time.Time { return r.clock },
		}}
	return r
}

var human = ticketing.Actor{Kind: ticketing.ActorHuman, ID: "iss|alice"}

func (r *rig) ticket(t *testing.T, n ticketing.NewTicket) *ticketing.Ticket {
	t.Helper()
	tk, err := r.p.Tickets().Create(context.Background(), n, human)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func agentTicket(title, agent string) ticketing.NewTicket {
	return ticketing.NewTicket{Title: title, Description: "do it", Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: agent}}
}

func (r *rig) run(t *testing.T, agent string) Outcome {
	t.Helper()
	out, err := r.d.RunOnce(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *rig) get(t *testing.T, id string) *ticketing.Ticket {
	t.Helper()
	tk, err := r.p.Tickets().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (r *rig) comments(t *testing.T, id string) []string {
	t.Helper()
	cs, err := r.p.Threads().Comments(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

func passed(id string) *model.GateRun { return &model.GateRun{ID: id, Status: model.GatePassed} }

// ---- worker matrix (test_wrapper.py / test_wrapper_modes.py parity) ----

func TestIdleSpendsNothing(t *testing.T) {
	r := newRig(t)
	if out := r.run(t, "beta"); out.Kind != OutcomeIdle || len(r.driver.prompts) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestAllowedAgentGetsTheTaskWithContract(t *testing.T) {
	r := newRig(t)
	r.driver.answer = "partial work"
	tk := r.ticket(t, agentTicket("Fix login", "beta"))
	out := r.run(t, "beta")
	if out.Kind != OutcomeRan || out.Status != ticketing.StatusInProgress {
		t.Fatalf("%+v", out)
	}
	p := r.driver.prompts[0]
	if !strings.Contains(p, "MEM-1: Fix login") || !strings.Contains(p, "do it") || !strings.Contains(p, "STATUS: DONE") || strings.Contains(p, "VERDICT") {
		t.Fatalf("prompt: %s", p)
	}
	if cs := r.comments(t, tk.Ref.ID); len(cs) != 1 || cs[0] != "partial work" {
		t.Fatalf("comments: %q", cs)
	}
	// Without a STATUS line the work stays in progress and the claim is kept:
	// the next heartbeat resumes it.
	if got := r.get(t, tk.Ref.ID); got.Status != ticketing.StatusInProgress || got.CheckedOutBy != "beta" {
		t.Fatalf("after partial: %+v", got)
	}
	if out := r.run(t, "beta"); out.Kind != OutcomeRan || len(r.driver.prompts) != 2 {
		t.Fatalf("resume: %+v", out)
	}
}

func TestBlockedAgentSpendsNoTurnAndCommentsOncePerReasonSet(t *testing.T) {
	r := newRig(t)
	tk := r.ticket(t, agentTicket("x", "beta"))
	block := func(code, run string) {
		r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: false, Action: "rework", Edges: []model.AdmissionEdge{
			{GateID: "tests", Code: code, RunID: run, Satisfied: false, Action: "rework", Message: "tests red"}}})
	}
	block("failed", "r1")
	for i := 0; i < 3; i++ {
		if out := r.run(t, "beta"); out.Kind != OutcomeBlocked {
			t.Fatalf("%+v", out)
		}
	}
	if len(r.driver.prompts) != 0 {
		t.Fatal("blocked agent spent a turn")
	}
	cs := r.comments(t, tk.Ref.ID)
	if len(cs) != 1 || !strings.Contains(cs[0], "Pipeline-Sperre fuer beta (Aktion: rework)") || !strings.Contains(cs[0], "- Gate tests: failed (Lauf r1) - tests red") {
		t.Fatalf("comments: %q", cs)
	}
	if got := r.get(t, tk.Ref.ID); got.Status != ticketing.StatusTodo || got.CheckedOutBy != "" {
		t.Fatalf("blocked ticket: %+v", got)
	}
	// A new reason set is reported again.
	block("failed", "r2")
	r.run(t, "beta")
	if cs := r.comments(t, tk.Ref.ID); len(cs) != 2 {
		t.Fatalf("new reason not reported: %q", cs)
	}
}

func TestDaemonDownOrInvalidAdmissionIsBlocked(t *testing.T) {
	for name, setup := range map[string]func(r *rig){
		"error":       func(r *rig) { r.admit.errs["beta"] = errors.New("dial unix: no such file") },
		"no agent id": func(r *rig) { r.admit.m["beta"] = &model.Admission{Allowed: true} },
		"inconsistent": func(r *rig) {
			r.admit.set("beta", &model.Admission{Allowed: true, Action: "allow", Edges: []model.AdmissionEdge{{Satisfied: false}}})
		},
		"deny action": func(r *rig) { r.admit.set("beta", &model.Admission{Allowed: true, Action: "deny"}) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			setup(r)
			r.ticket(t, agentTicket("x", "beta"))
			if out := r.run(t, "beta"); out.Kind != OutcomeBlocked || len(r.driver.prompts) != 0 {
				t.Fatalf("%+v prompts=%d", out, len(r.driver.prompts))
			}
		})
	}
}

func TestEndpoint409IsHandledLikeABlockedPrecheck(t *testing.T) {
	r := newRig(t)
	tk := r.ticket(t, agentTicket("x", "beta"))
	r.driver.err = &BlockedError{Admission: &model.Admission{AgentID: "beta", Agent: "beta", Action: "deny",
		Edges: []model.AdmissionEdge{{GateID: "tests", Code: "stale", Action: "deny"}}}}
	out := r.run(t, "beta")
	if out.Kind != OutcomeBlocked || !strings.Contains(out.Detail, "stale") {
		t.Fatalf("%+v", out)
	}
	if got := r.get(t, tk.Ref.ID); got.Status != ticketing.StatusTodo || got.CheckedOutBy != "" {
		t.Fatalf("after 409: %+v", got)
	}
}

func TestFailingPreRunGatePreventsTurn(t *testing.T) {
	r := newRig(t)
	tk := r.ticket(t, agentTicket("x", "beta"))
	r.gates.byPh["pre_run"] = &model.GateRun{ID: "p1", Status: model.GateFailed, Results: []model.GateResult{{ID: "lint", Status: "failed", Exit: 1, Output: "lint broke"}}}
	out := r.run(t, "beta")
	if out.Kind != OutcomePreRunFailed || len(r.driver.prompts) != 0 {
		t.Fatalf("%+v", out)
	}
	cs := r.comments(t, tk.Ref.ID)
	if len(cs) != 1 || !strings.Contains(cs[0], "pre_run-Gate fehlgeschlagen") || !strings.Contains(cs[0], "lint broke") {
		t.Fatalf("comment: %q", cs)
	}
	if got := r.get(t, tk.Ref.ID); got.Status != ticketing.StatusTodo || got.CheckedOutBy != "" {
		t.Fatalf("after pre_run: %+v", got)
	}
}

func TestDoneWithoutWorkspaceStaysOpen(t *testing.T) {
	r := newRig(t)
	r.d.Config.Workspace = func(context.Context, string) string { return "" }
	tk := r.ticket(t, agentTicket("x", "beta"))
	if out := r.run(t, "beta"); out.Status != ticketing.StatusInProgress {
		t.Fatalf("%+v", out)
	}
	if cs := r.comments(t, tk.Ref.ID); !strings.Contains(cs[0], "STATUS: DONE wird nicht akzeptiert") {
		t.Fatalf("%q", cs)
	}
}

func TestDoneDrivesGateAndWakesReleasedDownstreamOnce(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{ID: "e1", GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.gates.byPh["after_run,post_run"] = passed("g1")
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: true, Action: "allow", Edges: []model.AdmissionEdge{{GateID: "tests", UpstreamAgentID: "alpha", Satisfied: true, RunID: "g1", Action: "allow"}}})
	tk := r.ticket(t, agentTicket("x", "alpha"))
	out := r.run(t, "alpha")
	if out.Status != ticketing.StatusDone {
		t.Fatalf("%+v", out)
	}
	if got := r.get(t, tk.Ref.ID); got.Status != ticketing.StatusDone || got.CheckedOutBy != "" {
		t.Fatalf("done ticket: %+v", got)
	}
	call := r.gates.calls[len(r.gates.calls)-1]
	if call.UpstreamAgentID != "alpha" || call.Workspace != "/ws" || call.IssueID != tk.Ref.String() {
		t.Fatalf("gate call: %+v", call)
	}
	if len(r.waker.woken) != 1 || r.waker.woken[0] != "beta" {
		t.Fatalf("woken: %v", r.waker.woken)
	}
	if cs := r.comments(t, tk.Ref.ID); !strings.Contains(cs[0], "Pipeline:\nWAKE\tbeta\tallow") {
		t.Fatalf("%q", cs)
	}
	// The same gate run never wakes twice (wake is once per evidence).
	if _, err := r.d.route(context.Background(), "alpha", tk, passed("g1")); err != nil || len(r.waker.woken) != 1 {
		t.Fatalf("second route: %v woken=%v", err, r.waker.woken)
	}
}

func TestWakeActionTargetsNamedAgent(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: true, Action: "wake:gamma", Edges: []model.AdmissionEdge{{UpstreamAgentID: "alpha", Satisfied: true, RunID: "g1"}}})
	lines, err := r.d.route(context.Background(), "alpha", &ticketing.Ticket{}, passed("g1"))
	if err != nil || len(r.waker.woken) != 1 || r.waker.woken[0] != "gamma" || lines[0] != "WAKE\tgamma\twake:gamma" {
		t.Fatalf("%v %v %v", lines, err, r.waker.woken)
	}
}

func TestFailedGateOpensOneReworkTicketAndKeepsIssueOpen(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.gates.byPh["after_run,post_run"] = &model.GateRun{ID: "g2", Status: model.GateFailed, Results: []model.GateResult{{ID: "tests", Status: "failed", Exit: 1, Output: "1 failing"}}}
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: false, Action: "rework", Edges: []model.AdmissionEdge{
		{GateID: "tests", UpstreamAgentID: "alpha", Satisfied: false, Code: "failed", RunID: "g2", Action: "rework", Reason: "tests red"}}})
	tk := r.ticket(t, agentTicket("x", "alpha"))
	if out := r.run(t, "alpha"); out.Status != ticketing.StatusInProgress {
		t.Fatalf("%+v", out)
	}
	cs := r.comments(t, tk.Ref.ID)
	if !strings.Contains(cs[0], "STATUS: DONE wird nicht akzeptiert") || !strings.Contains(cs[0], "1 failing") || !strings.Contains(cs[0], "REWORK\ttests\tMEM-") {
		t.Fatalf("%q", cs)
	}
	reworks, _ := r.p.Tickets().List(context.Background(), ticketing.Filter{Query: "[gate tests / run g2]"})
	if len(reworks) != 1 || reworks[0].Assignee == nil || reworks[0].Assignee.ID != "alpha" {
		t.Fatalf("rework tickets: %+v", reworks)
	}
	// Re-routing the same run is idempotent (no second ticket, no second wake).
	if _, err := r.d.route(context.Background(), "alpha", tk, r.gates.byPh["after_run,post_run"]); err != nil {
		t.Fatal(err)
	}
	reworks, _ = r.p.Tickets().List(context.Background(), ticketing.Filter{Query: "[gate tests / run g2]"})
	if len(reworks) != 1 || len(r.waker.woken) != 1 {
		t.Fatalf("not idempotent: %d tickets, woken %v", len(reworks), r.waker.woken)
	}
}

func TestTamperBlocksDoneAndIsNeverReworked(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.gates.byPh["after_run,post_run"] = &model.GateRun{ID: "g3", Status: model.GatePassed, Tamper: true}
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: false, Action: "rework", Edges: []model.AdmissionEdge{
		{GateID: "tests", UpstreamAgentID: "alpha", Code: "tamper", RunID: "g3", Action: "rework"}}})
	tk := r.ticket(t, agentTicket("x", "alpha"))
	if out := r.run(t, "alpha"); out.Status != ticketing.StatusInProgress {
		t.Fatalf("%+v", out)
	}
	cs := r.comments(t, tk.Ref.ID)
	if !strings.Contains(cs[0], "DENY\tbeta\ttests\ttamper") || !strings.Contains(cs[0], "Manipulation erkannt") || strings.Contains(cs[0], "REWORK") {
		t.Fatalf("%q", cs)
	}
}

func TestRoutingFailureKeepsDoneAndNamesRetry(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.gates.byPh["after_run,post_run"] = passed("g1")
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: true, Action: "allow", Edges: []model.AdmissionEdge{{UpstreamAgentID: "alpha", Satisfied: true, RunID: "g1"}}})
	r.waker.err = errors.New("paperclip 500")
	tk := r.ticket(t, agentTicket("x", "alpha"))
	if out := r.run(t, "alpha"); out.Status != ticketing.StatusDone {
		t.Fatalf("routing failure discarded the verdict: %+v", out)
	}
	if cs := r.comments(t, tk.Ref.ID); !strings.Contains(cs[0], "Pipeline-Routing unvollstaendig") || !strings.Contains(cs[0], "dockweave ticket route") {
		t.Fatalf("%q", cs)
	}
	// A retry after the failure works (marker is "retry", not "done").
	r.waker.err = nil
	if _, err := r.d.route(context.Background(), "alpha", tk, passed("g1")); err != nil || len(r.waker.woken) != 1 {
		t.Fatalf("retry: %v %v", err, r.waker.woken)
	}
}

func TestUnknownWakeOutcomeWaitsForReconcileWindow(t *testing.T) {
	r := newRig(t)
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: true, Action: "allow", Edges: []model.AdmissionEdge{{UpstreamAgentID: "alpha", Satisfied: true, RunID: "g1"}}})
	r.waker.err = fmt.Errorf("%w: timeout", ticketing.ErrUnavailable)
	tk := &ticketing.Ticket{}
	if _, err := r.d.route(context.Background(), "alpha", tk, passed("g1")); err == nil {
		t.Fatal("expected failure")
	}
	r.waker.err = nil
	// Inside the window the unknown outcome is not repeated.
	if _, err := r.d.route(context.Background(), "alpha", tk, passed("g1")); err == nil || len(r.waker.woken) != 0 {
		t.Fatalf("retried within window: %v %v", err, r.waker.woken)
	}
	r.clock = r.clock.Add(11 * time.Minute)
	if _, err := r.d.route(context.Background(), "alpha", tk, passed("g1")); err != nil || len(r.waker.woken) != 1 {
		t.Fatalf("after window: %v %v", err, r.waker.woken)
	}
}

// creating creates the ticket but reports an unknown outcome once.
type lossyCreate struct {
	ticketing.Provider
	lose bool
}

func (l *lossyCreate) Tickets() ticketing.Tickets {
	return &lossyTickets{Tickets: l.Provider.Tickets(), p: l}
}

type lossyTickets struct {
	ticketing.Tickets
	p *lossyCreate
}

func (t *lossyTickets) Create(ctx context.Context, n ticketing.NewTicket, by ticketing.Actor) (*ticketing.Ticket, error) {
	tk, err := t.Tickets.Create(ctx, n, by)
	if err == nil && t.p.lose {
		t.p.lose = false
		return nil, fmt.Errorf("%w: response lost", ticketing.ErrUnavailable)
	}
	return tk, err
}

func TestUnknownReworkCreationIsReconciledByTag(t *testing.T) {
	r := newRig(t)
	lossy := &lossyCreate{Provider: r.p, lose: true}
	r.d.Tickets = lossy
	tk := &ticketing.Ticket{}
	if _, err := r.d.gateRework(context.Background(), "alpha", tk, "tests", "g9", "red"); err == nil {
		t.Fatal("expected unknown outcome")
	}
	// The ticket exists although the response was lost: found by its tag,
	// never created twice – even inside the reconcile window.
	ident, err := r.d.gateRework(context.Background(), "alpha", tk, "tests", "g9", "red")
	if err != nil || ident == "" {
		t.Fatalf("reconcile: %q %v", ident, err)
	}
	list, _ := r.p.Tickets().List(context.Background(), ticketing.Filter{Query: "[gate tests / run g9]"})
	if len(list) != 1 {
		t.Fatalf("duplicate rework tickets: %d", len(list))
	}
}

func TestOpenOrUnreadableBlockersRequeueWithoutTurn(t *testing.T) {
	r := newRig(t)
	dep := r.ticket(t, ticketing.NewTicket{Title: "dep"})
	n := agentTicket("after", "beta")
	n.BlockedBy = []ticketing.Ref{dep.Ref}
	tk := r.ticket(t, n)
	if out := r.run(t, "beta"); out.Kind != OutcomeWaiting || !strings.Contains(out.Detail, dep.Ref.ID+"=todo") || len(r.driver.prompts) != 0 {
		t.Fatalf("%+v", out)
	}
	done := ticketing.StatusDone
	_, _ = r.p.Tickets().Update(context.Background(), dep.Ref.ID, ticketing.Update{Status: &done}, human)
	if out := r.run(t, "beta"); out.Kind != OutcomeRan {
		t.Fatalf("after blocker done: %+v", out)
	}
	// An unreadable blocker fails closed.
	n2 := agentTicket("dangling", "gamma")
	n2.BlockedBy = []ticketing.Ref{{Provider: "memory", ID: "missing"}}
	r.ticket(t, n2)
	if out := r.run(t, "gamma"); out.Kind != OutcomeWaiting || !strings.Contains(out.Detail, "check-failed") {
		t.Fatalf("unreadable: %+v", out)
	}
	_ = tk
}

func TestReviewerFailOpensReworkForAuthorAndRecordsVerdict(t *testing.T) {
	r := newRig(t)
	work := r.ticket(t, agentTicket("Implement API", "backend"))
	n := agentTicket("Review API", "reviewer")
	n.BlockedBy = nil
	n.Description = "Pruefen.\nDEPENDS_ON: " + work.Ref.ID
	done := ticketing.StatusDone
	_, _ = r.p.Tickets().Update(context.Background(), work.Ref.ID, ticketing.Update{Status: &done}, human)
	rv := r.ticket(t, n)
	r.driver.answer = "looked at it\nVERDICT: FAIL\nREASON: tests missing\nSTATUS: DONE"
	out := r.run(t, "reviewer")
	if out.Status != ticketing.StatusInProgress || !strings.Contains(r.driver.prompts[0], "VERDICT: PASS") {
		t.Fatalf("%+v", out)
	}
	list, _ := r.p.Tickets().List(context.Background(), ticketing.Filter{Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "backend"}, Status: []ticketing.Status{ticketing.StatusTodo}})
	if len(list) != 1 || !strings.HasPrefix(list[0].Title, "Rework: Review API") || !strings.Contains(list[0].Description, "tests missing") {
		t.Fatalf("review rework: %+v", list)
	}
	if len(r.waker.woken) != 1 || r.waker.woken[0] != "backend" || len(r.rev.verdicts) != 1 || r.rev.verdicts[0] != rv.Ref.String()+"=FAIL" {
		t.Fatalf("wake/verdict: %v %v", r.waker.woken, r.rev.verdicts)
	}
}

func TestClaimConflictIsBusy(t *testing.T) {
	r := newRig(t)
	tk := r.ticket(t, agentTicket("x", "beta"))
	_ = r.p.Inbox().Checkout(context.Background(), tk.Ref.ID, "other")
	if out := r.run(t, "beta"); out.Kind != OutcomeIdle && out.Kind != OutcomeBusy {
		t.Fatalf("%+v", out)
	}
	if len(r.driver.prompts) != 0 {
		t.Fatal("turn spent on a ticket held by another agent")
	}
}

func TestEmptyAnswerNeverCountsAsDone(t *testing.T) {
	r := newRig(t)
	r.driver.answer = "   "
	tk := r.ticket(t, agentTicket("x", "beta"))
	if out := r.run(t, "beta"); out.Status != ticketing.StatusInProgress {
		t.Fatalf("%+v", out)
	}
	if cs := r.comments(t, tk.Ref.ID); cs[0] != "(keine Textantwort vom Agenten)" {
		t.Fatalf("%q", cs)
	}
}

func TestAgentFailureIsReportedAndRetried(t *testing.T) {
	r := newRig(t)
	r.driver.err = errors.New("agent_no_response")
	tk := r.ticket(t, agentTicket("x", "beta"))
	if out := r.run(t, "beta"); out.Kind != OutcomeAgentFailed {
		t.Fatalf("%+v", out)
	}
	if cs := r.comments(t, tk.Ref.ID); !strings.Contains(cs[0], "Agent-Lauf fehlgeschlagen: agent_no_response") {
		t.Fatalf("%q", cs)
	}
	r.driver.err = nil
	if out := r.run(t, "beta"); out.Kind != OutcomeRan || out.Status != ticketing.StatusDone {
		t.Fatalf("retry: %+v", out)
	}
}

func TestStatusContractParsing(t *testing.T) {
	for in, want := range map[string]ticketing.Status{
		"x\nSTATUS: DONE":                        ticketing.StatusDone,
		"status: blocked":                        ticketing.StatusBlocked,
		"STATUS: DONE\n...\nSTATUS: IN PROGRESS": ticketing.StatusInProgress,
		"nothing":                                ticketing.StatusInProgress,
		"STATUS: IN_PROGRESS then STATUS: DONE":  ticketing.StatusDone,
	} {
		if got := ParseStatus(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
	if ParseVerdict("VERDICT: PASS\nverdict: fail") != "FAIL" || ParseVerdict("no") != "" {
		t.Fatal("verdict")
	}
}

// Markers are durable: a restarted dispatcher (same store) never repeats a
// confirmed wake.
func TestActionMarkersSurviveRestart(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "dw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := newRig(t)
	r.d.Log = store
	r.edges.edges = []model.PipelineEdge{{GateID: "tests", UpstreamAgentID: "alpha", DownstreamAgentID: "beta"}}
	r.admit.set("beta", &model.Admission{Agent: "beta", Allowed: true, Action: "allow", Edges: []model.AdmissionEdge{{UpstreamAgentID: "alpha", Satisfied: true, RunID: "g1"}}})
	if _, err := r.d.route(context.Background(), "alpha", &ticketing.Ticket{}, passed("g1")); err != nil {
		t.Fatal(err)
	}
	restarted := &Dispatcher{Tickets: r.p, Admit: r.admit, Pipeline: r.edges, Waker: r.waker, Log: store, Config: r.d.Config}
	restarted.init()
	if _, err := restarted.route(context.Background(), "alpha", &ticketing.Ticket{}, passed("g1")); err != nil || len(r.waker.woken) != 1 {
		t.Fatalf("after restart: %v %v", err, r.waker.woken)
	}
}

// Two concurrent heartbeats never run the same ticket twice: the claim
// admits one; the other resumes nothing or finds the ticket held.
func TestConcurrentHeartbeatsSpendOneTurnPerTicket(t *testing.T) {
	r := newRig(t)
	gate := make(chan struct{})
	slow := &blockingDriver{release: gate}
	r.d.Driver = slow
	r.ticket(t, agentTicket("x", "beta"))
	r.ticket(t, agentTicket("y", "gamma"))
	var wg sync.WaitGroup
	outs := make(chan Outcome, 3)
	for _, a := range []string{"beta", "beta", "gamma"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			out, err := r.d.RunOnce(context.Background(), a)
			if err != nil {
				t.Error(err)
			}
			outs <- out
		}(a)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(outs)
	ran := 0
	for o := range outs {
		if o.Kind == OutcomeRan {
			ran++
		}
	}
	if slow.calls() > 3 || ran < 2 {
		t.Fatalf("turns=%d ran=%d", slow.calls(), ran)
	}
}

type blockingDriver struct {
	release chan struct{}
	mu      sync.Mutex
	perTk   map[string]int
	n       int
}

func (b *blockingDriver) Prompt(_ context.Context, _, ref, _ string) (string, error) {
	b.mu.Lock()
	b.n++
	if b.perTk == nil {
		b.perTk = map[string]int{}
	}
	b.perTk[ref]++
	b.mu.Unlock()
	<-b.release
	return "ok\nSTATUS: DONE", nil
}

func (b *blockingDriver) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range b.perTk {
		if n > 1 {
			return 99
		}
	}
	return b.n
}

func TestSchedulerTicksAndKicks(t *testing.T) {
	r := newRig(t)
	r.driver.answer = "ok\nSTATUS: DONE"
	r.ticket(t, agentTicket("a", "beta"))
	s := &Scheduler{D: r.d, Interval: 20 * time.Millisecond, Agents: func(context.Context) ([]string, error) { return []string{"beta"}, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.driver.mu.Lock()
		n := len(r.driver.prompts)
		r.driver.mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tick never ran the agent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A wake (kick) for another agent runs it without waiting for the tick list.
	r.ticket(t, agentTicket("b", "gamma"))
	if err := s.Wake(context.Background(), "gamma", "test"); err != nil {
		t.Fatal(err)
	}
	for {
		r.driver.mu.Lock()
		n := len(r.driver.prompts)
		r.driver.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("kick never ran gamma")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
