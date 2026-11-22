package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// Outcome kinds of one dispatcher step.
const (
	OutcomeIdle         = "idle"           // no work
	OutcomeWaiting      = "waiting"        // open blockers; re-queued, no turn
	OutcomeBlocked      = "blocked"        // pipeline admission refused; no turn
	OutcomeBusy         = "busy"           // another agent holds the ticket
	OutcomePreRunFailed = "pre_run_failed" // a pre_run gate failed; no turn
	OutcomeAgentFailed  = "agent_failed"   // the agent turn failed
	OutcomeRan          = "ran"            // a turn ran; Status is the result
)

// Outcome describes what one RunOnce did.
type Outcome struct {
	Kind   string           `json:"kind"`
	Ticket *ticketing.Ref   `json:"ticket,omitempty"`
	Status ticketing.Status `json:"status,omitempty"`
	Detail string           `json:"detail,omitempty"`
}

// Dispatcher runs the worker loop for one ticket provider.
type Dispatcher struct {
	Tickets  ticketing.Provider
	Admit    Admitter
	Pipeline PipelineEdges
	Gates    GateRunner
	Driver   Driver
	Waker    Waker
	Reviews  Reviews
	Log      ActionLog
	Config   Config
	Lead     LeadConfig

	once sync.Once
	acts *actions

	runMu   sync.Mutex
	running map[string]bool
}

// enter admits one heartbeat per agent at a time (ADR 0003 D-9): the claim
// alone cannot stop the same agent from taking its own ticket twice.
func (d *Dispatcher) enter(agentID string) bool {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	if d.running == nil {
		d.running = map[string]bool{}
	}
	if d.running[agentID] {
		return false
	}
	d.running[agentID] = true
	return true
}

func (d *Dispatcher) leave(agentID string) {
	d.runMu.Lock()
	delete(d.running, agentID)
	d.runMu.Unlock()
}

// init applies defaults once; RunOnce may run concurrently for several
// agents (scheduler), so configuration is never written afterwards.
func (d *Dispatcher) init() { d.once.Do(d.applyDefaults) }

func (d *Dispatcher) applyDefaults() {
	if d.Config.Now == nil {
		d.Config.Now = time.Now
	}
	if d.Config.ReconcileWindow == 0 {
		d.Config.ReconcileWindow = 600 * time.Second
	}
	if d.Config.PromptTimeout == 0 {
		d.Config.PromptTimeout = 240 * time.Second
	}
	if d.Config.BlockedStatus == "" {
		d.Config.BlockedStatus = string(ticketing.StatusTodo)
	}
	if d.acts == nil {
		d.acts = &actions{log: d.Log, window: d.Config.ReconcileWindow, now: d.Config.Now}
	}
}

// IssueKey is the ticket key stored with gate runs, reviews and live work.
// Paperclip tickets keep the bare issue id the wrapper always used, so the
// existing gate/review history stays valid without re-keying (ADR 0003 D-8);
// every other provider uses "<provider>:<id>".
func IssueKey(r ticketing.Ref) string {
	if r.Provider == "paperclip" {
		return r.ID
	}
	return r.String()
}

func agentActor(id string) ticketing.Actor {
	return ticketing.Actor{Kind: ticketing.ActorAgent, ID: id}
}

// RunOnce performs one heartbeat for agentID: the template method of ADR
// 0003 D-4. Every refusal before the turn spends no model turn.
func (d *Dispatcher) RunOnce(ctx context.Context, agentID string) (Outcome, error) {
	d.init()
	if !d.enter(agentID) {
		return Outcome{Kind: OutcomeBusy, Detail: "a heartbeat for " + agentID + " is already running"}, nil
	}
	defer d.leave(agentID)
	inbox := d.Tickets.Inbox()
	if inbox == nil {
		return Outcome{}, fmt.Errorf("%w: provider %s has no inbox", ticketing.ErrUnsupported, d.Tickets.Name())
	}
	tk, err := inbox.Next(ctx, agentID)
	if err != nil {
		return Outcome{}, err
	}
	if tk == nil {
		return Outcome{Kind: OutcomeIdle}, nil
	}
	ref := tk.Ref
	out := Outcome{Ticket: &ref}
	me := agentActor(agentID)

	// 1. Dependencies: open blockers (or an unreadable blocker) re-queue the
	// ticket without a turn (blocked-check.py, fail closed).
	if open, err := d.openBlockers(ctx, tk); err != nil || len(open) > 0 {
		detail := strings.Join(open, ",")
		if err != nil {
			detail = "check-failed: " + err.Error()
		}
		if tk.Status != ticketing.StatusTodo {
			st := ticketing.StatusTodo
			_, _ = d.Tickets.Tickets().Update(ctx, ref.ID, ticketing.Update{Status: &st}, me)
		}
		out.Kind, out.Detail = OutcomeWaiting, detail
		return out, nil
	}

	// 2. Pipeline admission (fail closed: an error is a refusal).
	adm, err := d.admission(ctx, agentID)
	if !adm.Allowed {
		d.reportBlocked(ctx, tk, adm, me)
		out.Kind, out.Detail = OutcomeBlocked, describe(adm)
		if err != nil {
			out.Detail += " (" + err.Error() + ")"
		}
		return out, nil
	}

	// 3. Claim: one agent per ticket.
	if err := inbox.Checkout(ctx, ref.ID, agentID); err != nil {
		if errors.Is(err, ticketing.ErrConflict) {
			out.Kind, out.Detail = OutcomeBusy, err.Error()
			return out, nil
		}
		return out, err
	}
	finalStatus := ticketing.StatusInProgress
	defer func() {
		// Keep the claim on partial work (resumed next heartbeat); give it
		// back otherwise. Providers whose claim is the status skip this.
		if finalStatus == ticketing.StatusInProgress {
			return
		}
		if cur, err := d.Tickets.Tickets().Get(context.WithoutCancel(ctx), ref.ID); err == nil && cur.CheckedOutBy == agentID {
			_ = inbox.Release(context.WithoutCancel(ctx), ref.ID, agentID)
		}
	}()

	workspace := ""
	if d.Config.Workspace != nil {
		workspace = d.Config.Workspace(ctx, tk.WorkspaceID)
	}
	// Lead mode: plan and delegate, or settle the delegated parent (no
	// pre_run gates, like the wrapper's orchestrate mode).
	if d.isLead(agentID) {
		var st ticketing.Status
		out, st = d.runLead(ctx, tk, agentID, workspace, out)
		finalStatus = st
		return out, nil
	}

	// 4. pre_run gates (only with a workspace; skipped without pre_run gates).
	if workspace != "" {
		run, err := d.Gates.Run(ctx, GateRequest{Workspace: workspace, IssueID: IssueKey(ref), Phases: []string{"pre_run"}})
		if !gateOK(run, err) {
			st := ticketing.StatusTodo
			finalStatus = st
			d.comment(ctx, ref.ID, "pre_run-Gate fehlgeschlagen; der Agent wurde nicht gestartet.\n\n"+gateReport(run, err), me)
			_, _ = d.Tickets.Tickets().Update(ctx, ref.ID, ticketing.Update{Status: &st}, me)
			out.Kind = OutcomePreRunFailed
			return out, nil
		}
	}

	// 5. Work starts: the ticket is in progress.
	if tk.Status != ticketing.StatusInProgress {
		st := ticketing.StatusInProgress
		_, _ = d.Tickets.Tickets().Update(ctx, ref.ID, ticketing.Update{Status: &st}, me)
	}
	reviewer := d.Config.Reviewer != nil && d.Config.Reviewer(agentID)
	pctx, cancel := context.WithTimeout(ctx, d.Config.PromptTimeout)
	content, err := d.Driver.Prompt(pctx, agentID, IssueKey(ref), BuildPrompt(tk, reviewer))
	cancel()
	if err != nil {
		var be *BlockedError
		if errors.As(err, &be) {
			// The endpoint refused despite the admission check (race): same
			// handling as a refused precheck (handle-409).
			a := be.Admission
			if a == nil {
				a = unavailable(agentID, "pipeline_blocked without admission")
			}
			a.Allowed = false
			d.reportBlocked(ctx, tk, a, me)
			finalStatus = ticketing.Status(d.Config.BlockedStatus)
			out.Kind, out.Detail = OutcomeBlocked, describe(a)
			return out, nil
		}
		d.comment(ctx, ref.ID, "Agent-Lauf fehlgeschlagen: "+truncate(err.Error(), 500), me)
		out.Kind, out.Detail = OutcomeAgentFailed, err.Error()
		return out, nil
	}

	// 6. Contracts and gates.
	status := ParseStatus(content)
	verdict := ParseVerdict(content)
	var notes []string
	if status == ticketing.StatusDone {
		if workspace == "" {
			status = ticketing.StatusInProgress
			notes = append(notes, "\nGate fehlgeschlagen - STATUS: DONE wird nicht akzeptiert.\n\n### Gate config\n```\nkein Workspace fuer dieses Ticket konfiguriert; das DONE-Gate kann nicht laufen\n```")
		} else {
			run, gerr := d.Gates.Run(ctx, GateRequest{Workspace: workspace, IssueID: IssueKey(ref), UpstreamAgentID: agentID,
				Phases: []string{"after_run", "post_run"}})
			lines, rerr := d.route(ctx, agentID, tk, run)
			if len(lines) > 0 {
				notes = append(notes, "\nPipeline:\n"+strings.Join(lines, "\n"))
			}
			if rerr != nil {
				// A routing failure never discards this ticket's gate verdict.
				notes = append(notes, fmt.Sprintf("\nPipeline-Routing unvollstaendig (%v). Folge-Agenten wurden ggf. nicht geweckt.\nWiederholen: dockweave ticket route %s --agent %s", rerr, ref.String(), agentID))
			}
			if !gateOK(run, gerr) {
				status = ticketing.StatusInProgress
				notes = append(notes, "\nGate fehlgeschlagen - STATUS: DONE wird nicht akzeptiert.\n"+gateReport(run, gerr))
			}
		}
	}
	if verdict == "FAIL" {
		status = ticketing.StatusInProgress
		reason := ParseReason(content)
		note := d.reviewRework(ctx, tk, reason)
		if d.Reviews != nil {
			_ = d.Reviews.RecordVerdict(ctx, IssueKey(ref), "FAIL", reason, agentID)
		}
		notes = append(notes, "\nRework: "+note)
	} else if verdict == "PASS" && d.Reviews != nil {
		_ = d.Reviews.RecordVerdict(ctx, IssueKey(ref), "PASS", ParseReason(content), agentID)
	}

	// 7. Record the answer and the resulting state.
	body := content
	if len(notes) > 0 {
		body += "\n" + strings.Join(notes, "\n")
	}
	if strings.TrimSpace(body) == "" {
		body = "(keine Textantwort vom Agenten)"
		if status == ticketing.StatusDone {
			status = ticketing.StatusInProgress
		}
	}
	d.comment(ctx, ref.ID, body, me)
	if _, err := d.Tickets.Tickets().Update(ctx, ref.ID, ticketing.Update{Status: &status}, me); err != nil {
		out.Detail = "status update failed: " + err.Error()
	}
	finalStatus = status
	out.Kind, out.Status = OutcomeRan, status
	// A finished child unblocks its siblings; the last one wakes the Lead,
	// whose heartbeat closes the parent (wake-ready.py / parent-close.py).
	if tk.Parent != nil && status.Closed() {
		d.wakeReady(ctx, *tk.Parent)
		if kids, err := d.children(ctx, *tk.Parent); err == nil {
			all := true
			for _, k := range kids {
				if k.Ref == ref {
					continue
				}
				all = all && k.Status.Closed()
			}
			if parent, err := d.Tickets.Tickets().Get(ctx, tk.Parent.ID); all && err == nil && parent.Assignee != nil && parent.Assignee.Kind == ticketing.ActorAgent {
				lead := parent.Assignee.ID
				_, _ = d.acts.once(ctx, "children-done|"+tk.Parent.String()+"|"+parent.Version, func() (string, error) {
					return "", d.wake(ctx, lead, "Alle Child-Issues erledigt")
				})
			}
		}
	}
	return out, nil
}

func (d *Dispatcher) comment(ctx context.Context, id, body string, by ticketing.Actor) {
	if th := d.Tickets.Threads(); th != nil {
		_, _ = th.AddComment(ctx, id, body, by)
	}
}

func (d *Dispatcher) openBlockers(ctx context.Context, tk *ticketing.Ticket) ([]string, error) {
	var open []string
	for _, b := range tk.BlockedBy {
		bt, err := d.Tickets.Tickets().Get(ctx, b.ID)
		if err != nil {
			return nil, fmt.Errorf("blocker %s: %w", b, err)
		}
		if !bt.Status.Closed() {
			open = append(open, b.ID+"="+string(bt.Status))
		}
	}
	return open, nil
}

// ---- admission ----

func unavailable(agent, reason string) *model.Admission {
	return &model.Admission{AgentID: agent, Agent: agent, Allowed: false, Action: "deny", Edges: []model.AdmissionEdge{{
		Code: "unavailable", Satisfied: false, Action: "deny", Message: "pipeline admission unavailable: " + truncate(reason, 300)}}}
}

// admission never returns nil; an error or an inconsistent answer is a
// refusal (valid_admission in pipeline.py).
func (d *Dispatcher) admission(ctx context.Context, agent string) (*model.Admission, error) {
	if d.Admit == nil {
		return unavailable(agent, "no admission source"), errors.New("no admission source")
	}
	adm, err := d.Admit.Admit(ctx, agent)
	if err != nil {
		return unavailable(agent, err.Error()), err
	}
	if adm == nil || adm.AgentID == "" {
		return unavailable(agent, "invalid answer"), errors.New("invalid admission")
	}
	if adm.Allowed {
		for _, e := range adm.Edges {
			if !e.Satisfied {
				adm.Allowed = false
			}
		}
		if adm.Action != "" && adm.Action != "allow" && !strings.HasPrefix(adm.Action, "wake:") {
			adm.Allowed = false
		}
	}
	return adm, nil
}

func blockingEdges(adm *model.Admission) []model.AdmissionEdge {
	var out []model.AdmissionEdge
	for _, e := range adm.Edges {
		if !e.Satisfied {
			out = append(out, e)
		}
	}
	return out
}

func describe(adm *model.Admission) string {
	name := adm.Agent
	if name == "" {
		name = adm.AgentID
	}
	action := adm.Action
	if action == "" {
		action = "deny"
	}
	lines := []string{fmt.Sprintf("Pipeline-Sperre fuer %s (Aktion: %s):", name, action)}
	for _, e := range blockingEdges(adm) {
		l := fmt.Sprintf("- Gate %s: %s", orDash(e.GateID), orQ(e.Code))
		if e.RunID != "" {
			l += " (Lauf " + e.RunID + ")"
		}
		if e.Message != "" {
			l += " - " + e.Message
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// reportBlocked comments once per reason set and always re-queues the
// ticket (report_blocked in pipeline.py).
func (d *Dispatcher) reportBlocked(ctx context.Context, tk *ticketing.Ticket, adm *model.Admission, me ticketing.Actor) {
	var codes []string
	for _, e := range blockingEdges(adm) {
		codes = append(codes, fmt.Sprintf("%s=%s:%s", e.GateID, e.Code, e.RunID))
	}
	sort.Strings(codes)
	key := "blocked|" + tk.Ref.String() + "|" + strings.Join(codes, ",")
	text := describe(adm) + "\n\nDer Agent arbeitet erst, wenn das Gate auf dem aktuellen Stand bestanden ist."
	_, err := d.acts.once(ctx, key, func() (string, error) {
		th := d.Tickets.Threads()
		if th == nil {
			return "", nil
		}
		c, err := th.AddComment(ctx, tk.Ref.ID, text, me)
		if err != nil {
			return "", err
		}
		return c.ID, nil
	})
	_ = err // unknown outcome: status update only, no duplicate comment
	// Compare with the current state: the run may have moved it already.
	st := ticketing.Status(d.Config.BlockedStatus)
	if cur, err := d.Tickets.Tickets().Get(ctx, tk.Ref.ID); err != nil || cur.Status != st {
		_, _ = d.Tickets.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st}, me)
	}
}

// ---- gates ----

func gateOK(run *model.GateRun, err error) bool {
	if err != nil || run == nil || run.Tamper {
		return false
	}
	return run.Status == model.GatePassed || run.Status == model.GateSkipped
}

func gateReport(run *model.GateRun, err error) string {
	if err != nil {
		return "### Gate gate-runner\n```\n" + truncate(err.Error(), 2000) + "\n```"
	}
	if run == nil {
		return "### Gate gate-runner\n```\nkeine Antwort vom Gate-Runner\n```"
	}
	var b strings.Builder
	if run.Tamper {
		b.WriteString("### Gate tamper\n```\nManipulation erkannt (Gate-Konfiguration oder geschuetzte Dateien geaendert)\n```\n")
	}
	for _, r := range run.Results {
		if r.Status == "passed" {
			continue
		}
		fmt.Fprintf(&b, "\n### Gate %s (exit %d)\n```\n%s\n```", r.ID, r.Exit, truncate(firstNonEmpty(r.Output, r.Reason), 2000))
	}
	if b.Len() == 0 && run.Reason != "" {
		fmt.Fprintf(&b, "### Gate %s\n```\n%s\n```", run.Status, truncate(run.Reason, 2000))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
