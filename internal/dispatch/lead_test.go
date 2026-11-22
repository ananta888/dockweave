package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ananta888/dockweave/internal/ticketing"
)

type agentDriver struct {
	mu      sync.Mutex
	answers map[string]string
	calls   map[string]int
}

func (a *agentDriver) Prompt(_ context.Context, agent, _, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls == nil {
		a.calls = map[string]int{}
	}
	a.calls[agent]++
	return a.answers[agent], nil
}

const planAnswer = "Hier der Plan:\n```json\n" + `{"tasks":[
 {"title":"API bauen","description":"REST API","role":"backend","depends_on":[]},
 {"title":"UI bauen","description":"Angular","role":"frontend","depends_on":["API bauen"]},
 {"title":"Review","description":"pruefen","role":"reviewer","depends_on":["API bauen","UI bauen"]},
 {"title":"Docs","description":"x","role":"writer"}]}` + "\n```"

func leadRig(t *testing.T) (*rig, *agentDriver, map[string]bool) {
	r := newRig(t)
	drv := &agentDriver{answers: map[string]string{
		"lead":     planAnswer,
		"backend":  "api done\nSTATUS: DONE",
		"frontend": "ui done\nSTATUS: DONE",
		"reviewer": "alles gut\nVERDICT: PASS\nREASON: ok\nSTATUS: DONE",
	}}
	approved := map[string]bool{}
	r.d.Driver = drv
	r.d.Config.Reviewer = func(a string) bool { return a == "reviewer" }
	r.d.Lead = LeadConfig{
		IsLead: func(a string) bool { return a == "lead" },
		Roles:  map[string]string{"backend": "backend", "frontend": "frontend", "reviewer": "reviewer"},
		Policy: func(context.Context, string) LeadPolicy { return LeadPolicy{ReviewRequired: true} },
		Approved: func(_ context.Context, key string) bool {
			return approved[key]
		},
	}
	return r, drv, approved
}

func TestExtractPlan(t *testing.T) {
	if p := ExtractPlan(planAnswer); len(p) != 4 || p[1].DependsOn[0] != "API bauen" {
		t.Fatalf("fenced: %+v", p)
	}
	if p := ExtractPlan(`Prosa davor {"tasks":[{"title":"a","role":"backend"}]} und danach`); len(p) != 1 {
		t.Fatalf("bare: %+v", p)
	}
	for _, s := range []string{"", "kein plan", "```json\n{\"tasks\": []}\n```", `{"tasks":"x"}`} {
		if p := ExtractPlan(s); len(p) != 0 {
			t.Fatalf("%q: %+v", s, p)
		}
	}
}

// Full Lead cycle: plan → children with blockers → workers in dependency
// order → attributed review PASS → parent closed (TK-014).
func TestLeadPlansDelegatesAndClosesParent(t *testing.T) {
	r, drv, _ := leadRig(t)
	ctx := context.Background()
	goal := r.ticket(t, agentTicket("Produkt bauen", "lead"))
	out := r.run(t, "lead")
	if out.Kind != OutcomeRan || out.Status != ticketing.StatusInProgress {
		t.Fatalf("lead: %+v", out)
	}
	kids, _ := r.p.Tickets().List(ctx, ticketing.Filter{Parent: &goal.Ref})
	if len(kids) != 3 {
		t.Fatalf("children: %d", len(kids))
	}
	by := map[string]ticketing.Ticket{}
	for _, k := range kids {
		by[k.Title] = k
	}
	api, ui, rv := by["API bauen"], by["UI bauen"], by["Review"]
	if api.Assignee.ID != "backend" || len(ui.BlockedBy) != 1 || ui.BlockedBy[0] != api.Ref || len(rv.BlockedBy) != 2 {
		t.Fatalf("wiring: api=%+v ui=%+v rv=%+v", api, ui, rv)
	}
	if !strings.Contains(ui.Description, "Arbeitsverzeichnis: /workspace") || !strings.Contains(ui.Description, "- API bauen") {
		t.Fatalf("description: %s", ui.Description)
	}
	cs := r.comments(t, goal.Ref.ID)
	if !strings.Contains(cs[len(cs)-1], "Lead-Plan erstellt") || !strings.Contains(cs[len(cs)-1], "SKIP\twriter") {
		t.Fatalf("plan comment: %q", cs)
	}
	if uc := r.comments(t, ui.Ref.ID); len(uc) != 1 || !strings.HasPrefix(uc[0], "DEPENDS_ON: ") {
		t.Fatalf("marker: %q", uc)
	}
	// Only the unblocked child is woken.
	if len(r.waker.woken) != 1 || r.waker.woken[0] != "backend" {
		t.Fatalf("woken after plan: %v", r.waker.woken)
	}
	// A second Lead heartbeat never plans again.
	r.run(t, "lead")
	if drv.calls["lead"] != 1 {
		t.Fatalf("lead planned twice: %d", drv.calls["lead"])
	}
	// The UI child waits for the API child.
	if out := r.run(t, "frontend"); out.Kind != OutcomeWaiting {
		t.Fatalf("frontend before api: %+v", out)
	}
	r.gates.byPh["after_run,post_run"] = passed("g1")
	if out := r.run(t, "backend"); out.Status != ticketing.StatusDone {
		t.Fatalf("backend: %+v", out)
	}
	if !containsStr(r.waker.woken, "frontend") {
		t.Fatalf("frontend not woken: %v", r.waker.woken)
	}
	if out := r.run(t, "frontend"); out.Status != ticketing.StatusDone {
		t.Fatalf("frontend: %+v", out)
	}
	if out := r.run(t, "reviewer"); out.Status != ticketing.StatusDone {
		t.Fatalf("reviewer: %+v", out)
	}
	if !containsStr(r.waker.woken, "lead") {
		t.Fatalf("lead not woken after the last child: %v", r.waker.woken)
	}
	out = r.run(t, "lead")
	if out.Status != ticketing.StatusDone || !strings.Contains(out.Detail, "PARENT done") {
		t.Fatalf("parent close: %+v", out)
	}
	if g := r.get(t, goal.Ref.ID); g.Status != ticketing.StatusDone {
		t.Fatalf("goal: %+v", g)
	}
	if !containsStr(r.rev.verdicts, IssueKey(rv.Ref)+"=PASS") {
		t.Fatalf("review verdict not recorded: %v", r.rev.verdicts)
	}
}

func containsStr(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func TestParentWaitsForAttributedPassAndHumanApproval(t *testing.T) {
	r, _, approved := leadRig(t)
	ctx := context.Background()
	r.d.Lead.Policy = func(context.Context, string) LeadPolicy {
		return LeadPolicy{ReviewRequired: true, HumanApproval: true}
	}
	goal := r.ticket(t, agentTicket("Goal", "lead"))
	ref := goal.Ref
	done := ticketing.StatusDone
	review := r.ticket(t, ticketing.NewTicket{Title: "Review", Parent: &ref, Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "reviewer"}})
	_, _ = r.p.Tickets().Update(ctx, review.Ref.ID, ticketing.Update{Status: &done}, human)
	// An unattributed (system) PASS does not count.
	_, _ = r.p.Threads().AddComment(ctx, review.Ref.ID, "VERDICT: PASS", ticketing.Actor{Kind: ticketing.ActorSystem})
	if out := r.run(t, "lead"); !strings.Contains(out.Detail, "not attributed") {
		t.Fatalf("unattributed: %+v", out)
	}
	_, _ = r.p.Threads().AddComment(ctx, review.Ref.ID, "VERDICT: PASS", ticketing.Actor{Kind: ticketing.ActorAgent, ID: "reviewer"})
	out := r.run(t, "lead")
	if out.Status != ticketing.StatusInReview || r.get(t, goal.Ref.ID).Status != ticketing.StatusInReview {
		t.Fatalf("approval missing: %+v", out)
	}
	r.run(t, "lead")
	n := 0
	for _, c := range r.comments(t, goal.Ref.ID) {
		if strings.Contains(c, "DockWeave-Freigabe") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("approval request commented %d times", n)
	}
	approved[IssueKey(goal.Ref)] = true
	// The approval puts the parent back in progress and wakes the Lead.
	if err := r.d.Approved(ctx, IssueKey(goal.Ref)); err != nil {
		t.Fatal(err)
	}
	if !containsStr(r.waker.woken, "lead") {
		t.Fatalf("lead not woken by the approval: %v", r.waker.woken)
	}
	if out := r.run(t, "lead"); out.Status != ticketing.StatusDone {
		t.Fatalf("after approval: %+v", out)
	}
}

func TestLeadWithoutPlanKeepsGoalOpen(t *testing.T) {
	r, drv, _ := leadRig(t)
	drv.answers["lead"] = "Ich habe keine Idee."
	goal := r.ticket(t, agentTicket("Goal", "lead"))
	if out := r.run(t, "lead"); out.Status != ticketing.StatusInProgress || out.Detail != "no plan" {
		t.Fatalf("%+v", out)
	}
	if cs := r.comments(t, goal.Ref.ID); !strings.Contains(cs[0], "Kein JSON-Plan") {
		t.Fatalf("%q", cs)
	}
}
