package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ananta888/dockweave/internal/ticketing"
)

// LeadPolicy is the parent-close policy of a workspace (.paperclip/gates.json:
// review / human_approval). Review defaults to required like parent-close.py.
type LeadPolicy struct {
	ReviewRequired bool
	ReviewMatch    string // child title substring, default "review"
	HumanApproval  bool
}

// LeadConfig enables the Lead/orchestration mode (TK-014; port of the
// orchestrate branch of dockweave-run.sh, plan-extract.py, plan-delegate.py,
// wake-ready.py and parent-close.py).
type LeadConfig struct {
	// IsLead reports whether an agent plans instead of implementing.
	IsLead func(agentID string) bool
	// Roles maps plan roles (backend, frontend, reviewer, platform, …) to
	// DockWeave agents.
	Roles map[string]string
	// ContainerWorkspace is the shared working directory named in the plan
	// prompt and in child descriptions (default /workspace).
	ContainerWorkspace string
	// Policy reads the parent-close policy for a host workspace.
	Policy func(ctx context.Context, workspace string) LeadPolicy
	// Approved reports a DockWeave-side human approval for a ticket key.
	Approved func(ctx context.Context, issueKey string) bool
}

const leadContract = `

Du bist der Lead/CEO des DockWeave-Engineering-Teams. Zerlege das Goal in konkrete Child-Issues fuer die Rollen %s.
Gemeinsames Arbeitsverzeichnis: %s

Antworte NUR mit einem JSON-Objekt in einem ` + "```json" + `-Block:
{"tasks":[{"title":"...","description":"...","role":"backend","depends_on":[]}]}
Jede Aufgabe braucht title, description und role. Keine Prosa ausserhalb des JSON-Blocks.
`

// PlanTask is one entry of the Lead's work breakdown.
type PlanTask struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Role        string   `json:"role"`
	DependsOn   []string `json:"depends_on"`
}

var fencedRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// ExtractPlan finds the JSON plan in an answer: a fenced block first, then
// the first bare object with a "tasks" list (plan-extract.py).
func ExtractPlan(raw string) []PlanTask {
	parse := func(s string) ([]PlanTask, bool) {
		var obj struct {
			Tasks []json.RawMessage `json:"tasks"`
		}
		if json.Unmarshal([]byte(s), &obj) != nil || obj.Tasks == nil {
			return nil, false
		}
		var out []PlanTask
		for _, t := range obj.Tasks {
			var pt PlanTask
			if json.Unmarshal(t, &pt) == nil && (pt.Title != "" || pt.Role != "") {
				out = append(out, pt)
			}
		}
		return out, true
	}
	for _, m := range fencedRe.FindAllStringSubmatch(raw, -1) {
		b := strings.TrimSpace(m[1])
		if strings.HasPrefix(b, "{") && strings.HasSuffix(b, "}") {
			if p, ok := parse(b); ok && len(p) > 0 {
				return p
			}
		}
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(raw[i:]))
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			continue
		}
		if p, ok := parse(string(v)); ok && len(p) > 0 {
			return p
		}
	}
	return nil
}

func (d *Dispatcher) isLead(agentID string) bool {
	return d.Lead.IsLead != nil && d.Lead.IsLead(agentID)
}

func (d *Dispatcher) children(ctx context.Context, parent ticketing.Ref) ([]ticketing.Ticket, error) {
	return d.Tickets.Tickets().List(ctx, ticketing.Filter{Parent: &parent})
}

// runLead handles a Lead ticket after admission and claim: settle an
// already delegated parent, or plan, delegate and wake what is ready.
func (d *Dispatcher) runLead(ctx context.Context, tk *ticketing.Ticket, agentID, workspace string, out Outcome) (Outcome, ticketing.Status) {
	me := agentActor(agentID)
	kids, err := d.children(ctx, tk.Ref)
	if err != nil {
		out.Kind, out.Detail = OutcomeAgentFailed, "children: "+err.Error()
		return out, ticketing.StatusInProgress
	}
	if len(kids) > 0 {
		// Already delegated: no new plan, only settle the parent.
		st, detail := d.closeParent(ctx, tk, kids, workspace, me)
		out.Kind, out.Status, out.Detail = OutcomeRan, st, detail
		return out, st
	}
	if tk.Status != ticketing.StatusInProgress {
		st := ticketing.StatusInProgress
		_, _ = d.Tickets.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st}, me)
	}
	roles := make([]string, 0, len(d.Lead.Roles))
	for r := range d.Lead.Roles {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	cw := firstNonEmpty(d.Lead.ContainerWorkspace, "/workspace")
	prompt := strings.TrimSuffix(BuildPrompt(tk, false), statusContract) + fmt.Sprintf(leadContract, strings.Join(roles, ", "), cw)
	pctx, cancel := context.WithTimeout(ctx, d.Config.PromptTimeout)
	content, err := d.Driver.Prompt(pctx, agentID, IssueKey(tk.Ref), prompt)
	cancel()
	if err != nil {
		var be *BlockedError
		if errors.As(err, &be) {
			a := be.Admission
			if a == nil {
				a = unavailable(agentID, "pipeline_blocked without admission")
			}
			d.reportBlocked(ctx, tk, a, me)
			out.Kind, out.Detail = OutcomeBlocked, describe(a)
			return out, ticketing.Status(d.Config.BlockedStatus)
		}
		d.comment(ctx, tk.Ref.ID, "Agent-Lauf fehlgeschlagen: "+truncate(err.Error(), 500), me)
		out.Kind, out.Detail = OutcomeAgentFailed, err.Error()
		return out, ticketing.StatusInProgress
	}
	plan := ExtractPlan(content)
	if len(plan) == 0 {
		d.comment(ctx, tk.Ref.ID, content+"\n\nKein JSON-Plan in der Lead-Antwort; das Goal bleibt offen.", me)
		out.Kind, out.Status, out.Detail = OutcomeRan, ticketing.StatusInProgress, "no plan"
		return out, ticketing.StatusInProgress
	}
	lines := d.delegate(ctx, tk, plan, cw)
	d.comment(ctx, tk.Ref.ID, "Lead-Plan erstellt. Child-Issues:\n"+strings.Join(lines, "\n")+"\n\n"+content, me)
	d.wakeReady(ctx, tk.Ref)
	out.Kind, out.Status, out.Detail = OutcomeRan, ticketing.StatusInProgress, fmt.Sprintf("%d child line(s)", len(lines))
	return out, ticketing.StatusInProgress
}

// delegate creates one child per plan task (reusing children with the same
// title), then wires the dependencies as blocked_by (plan-delegate.py).
func (d *Dispatcher) delegate(ctx context.Context, parent *ticketing.Ticket, plan []PlanTask, cw string) []string {
	existing, _ := d.children(ctx, parent.Ref)
	byTitle := map[string]ticketing.Ticket{}
	for _, k := range existing {
		byTitle[strings.TrimSpace(k.Title)] = k
	}
	var lines []string
	created := map[string]ticketing.Ticket{}
	for _, t := range plan {
		role := strings.ToLower(strings.TrimSpace(t.Role))
		title := strings.TrimSpace(t.Title)
		agent := d.Lead.Roles[role]
		if agent == "" || title == "" {
			lines = append(lines, "SKIP\t"+firstNonEmpty(role, "?")+"\tmissing role or agent")
			continue
		}
		if k, ok := byTitle[title]; ok {
			created[title] = k
			lines = append(lines, fmt.Sprintf("CHILD\t%s\t%s\treused", firstNonEmpty(k.Identifier, k.Ref.ID), role))
			continue
		}
		desc := []string{"Arbeitsverzeichnis: " + cw, ""}
		if b := strings.TrimSpace(t.Description); b != "" {
			desc = append(desc, b)
		}
		if deps := cleanDeps(t.DependsOn); len(deps) > 0 {
			desc = append(desc, "", "Abhaengigkeiten (erst nach diesen Issues abschliessen):")
			for _, dep := range deps {
				desc = append(desc, "- "+dep)
			}
		}
		desc = append(desc, "", "Regeln: nur Dateien unter "+cw+" aendern, keine parallelen Schreibzugriffe",
			"auf dieselben Dateien, Aenderungen klein halten und am Ende kurz zusammenfassen,", "was fertig ist und was offen bleibt.")
		ref := parent.Ref
		child, err := d.Tickets.Tickets().Create(ctx, ticketing.NewTicket{Title: title, Description: strings.Join(desc, "\n"),
			Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: agent}, Parent: &ref, WorkspaceID: parent.WorkspaceID},
			agentActor(firstNonEmpty(d.leadOf(parent), "lead")))
		if err != nil {
			lines = append(lines, "ERR\t"+role+"\t"+truncate(err.Error(), 200))
			continue
		}
		created[title] = *child
		lines = append(lines, fmt.Sprintf("CHILD\t%s\t%s\tcreated", firstNonEmpty(child.Identifier, child.Ref.ID), role))
	}
	// Second pass: real blockers plus the machine-readable marker.
	for _, t := range plan {
		child, ok := created[strings.TrimSpace(t.Title)]
		if !ok {
			continue
		}
		var refs []ticketing.Ref
		var idents []string
		for _, dep := range cleanDeps(t.DependsOn) {
			if c, ok := created[dep]; ok {
				refs = append(refs, c.Ref)
				idents = append(idents, firstNonEmpty(c.Identifier, c.Ref.ID))
			}
		}
		if len(refs) == 0 {
			continue
		}
		if _, err := d.Tickets.Tickets().Update(ctx, child.Ref.ID, ticketing.Update{BlockedBy: &refs}, ticketing.Actor{Kind: ticketing.ActorSystem}); err != nil {
			lines = append(lines, "ERR\tdeps\t"+truncate(t.Title, 40)+"\t"+truncate(err.Error(), 160))
		}
		if th := d.Tickets.Threads(); th != nil {
			cs, _ := th.Comments(ctx, child.Ref.ID)
			has := false
			for _, c := range cs {
				has = has || strings.Contains(c.Body, "DEPENDS_ON:")
			}
			if !has {
				_, _ = th.AddComment(ctx, child.Ref.ID, "DEPENDS_ON: "+strings.Join(idents, ",")+"\n(automatisch gesetzt aus dem Lead-Plan)", ticketing.Actor{Kind: ticketing.ActorSystem})
			}
		}
	}
	return lines
}

func (d *Dispatcher) leadOf(t *ticketing.Ticket) string {
	if t.Assignee != nil && t.Assignee.Kind == ticketing.ActorAgent {
		return t.Assignee.ID
	}
	return ""
}

func cleanDeps(in []string) []string {
	var out []string
	for _, d := range in {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// wakeReady wakes the assignees of open children whose blockers are closed
// (wake-ready.py); each ticket version is woken at most once.
func (d *Dispatcher) wakeReady(ctx context.Context, parent ticketing.Ref) []string {
	kids, err := d.children(ctx, parent)
	if err != nil {
		return nil
	}
	status := map[string]ticketing.Status{}
	for _, k := range kids {
		status[k.Ref.ID] = k.Status
	}
	var woke []string
	for _, k := range kids {
		if (k.Status != ticketing.StatusTodo && k.Status != ticketing.StatusBlocked) || k.Assignee == nil || k.Assignee.Kind != ticketing.ActorAgent {
			continue
		}
		ready := true
		for _, b := range k.BlockedBy {
			st, ok := status[b.ID]
			if !ok {
				if bt, err := d.Tickets.Tickets().Get(ctx, b.ID); err == nil {
					st, ok = bt.Status, true
				}
			}
			if !ok || !st.Closed() {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		agent := k.Assignee.ID
		if _, err := d.acts.once(ctx, "ready|"+k.Ref.String()+"|"+k.Version, func() (string, error) {
			return "", d.wake(ctx, agent, "Unblocked: "+firstNonEmpty(k.Identifier, k.Ref.ID))
		}); err == nil {
			woke = append(woke, agent)
		}
	}
	return woke
}

// closeParent closes a Lead ticket only when every child is finished, the
// review child carries an attributed PASS and – if required – a DockWeave
// human approval exists (parent-close.py). Returns the parent's status.
func (d *Dispatcher) closeParent(ctx context.Context, tk *ticketing.Ticket, kids []ticketing.Ticket, workspace string, me ticketing.Actor) (ticketing.Status, string) {
	for _, k := range kids {
		if !k.Status.Closed() {
			d.wakeReady(ctx, tk.Ref)
			return ticketing.StatusInProgress, "waiting for children"
		}
	}
	pol := LeadPolicy{ReviewRequired: true}
	if d.Lead.Policy != nil && workspace != "" {
		pol = d.Lead.Policy(ctx, workspace)
	}
	match := strings.ToLower(firstNonEmpty(pol.ReviewMatch, "review"))
	if pol.ReviewRequired {
		var reviews []ticketing.Ticket
		for _, k := range kids {
			if strings.Contains(strings.ToLower(k.Title), match) {
				reviews = append(reviews, k)
			}
		}
		if len(reviews) == 0 {
			return ticketing.StatusInProgress, "WAITING review: no review child"
		}
		for _, rv := range reviews {
			if ok, why := d.attributedPass(ctx, rv); !ok {
				return ticketing.StatusInProgress, "WAITING review: " + why + " (" + firstNonEmpty(rv.Identifier, rv.Ref.ID) + ")"
			}
			if d.Reviews != nil {
				_ = d.Reviews.RecordVerdict(ctx, IssueKey(rv.Ref), "PASS", "attributed PASS on the review child", d.leadOf(&rv))
			}
		}
	}
	if pol.HumanApproval && (d.Lead.Approved == nil || !d.Lead.Approved(ctx, IssueKey(tk.Ref))) {
		st := ticketing.StatusInReview
		_, _ = d.acts.once(ctx, "await-approval|"+tk.Ref.String(), func() (string, error) {
			d.comment(ctx, tk.Ref.ID, "Alle Kinder gruen. Warte auf: DockWeave-Freigabe (`dockweave gate approve "+IssueKey(tk.Ref)+"`).", me)
			return "", nil
		})
		if tk.Status != st {
			_, _ = d.Tickets.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st}, me)
		}
		return st, "WAITING human approval"
	}
	lines := []string{"Alle Child-Issues sind abgeschlossen:"}
	sort.Slice(kids, func(i, j int) bool { return kids[i].Identifier < kids[j].Identifier })
	for _, k := range kids {
		lines = append(lines, fmt.Sprintf("- %s %s (%s)", firstNonEmpty(k.Identifier, k.Ref.ID), k.Title, k.Status))
	}
	if pol.ReviewRequired {
		lines = append(lines, "\nReview: VERDICT PASS")
	}
	if pol.HumanApproval {
		lines = append(lines, "Menschliche Freigabe: erteilt")
	}
	d.comment(ctx, tk.Ref.ID, strings.Join(lines, "\n"), me)
	st := ticketing.StatusDone
	_, _ = d.Tickets.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st}, me)
	return st, "PARENT done"
}

// attributedPass: a "VERDICT: PASS" comment from the review child's assigned
// agent or a named human counts; system or unattributed PASS does not.
func (d *Dispatcher) attributedPass(ctx context.Context, rv ticketing.Ticket) (bool, string) {
	th := d.Tickets.Threads()
	if th == nil {
		return false, "no threads"
	}
	cs, err := th.Comments(ctx, rv.Ref.ID)
	if err != nil {
		return false, err.Error()
	}
	seenOther := false
	for _, c := range cs {
		if !strings.Contains(strings.ToUpper(c.Body), "VERDICT: PASS") {
			continue
		}
		if c.Author.Kind == ticketing.ActorAgent && rv.Assignee != nil && c.Author.ID == rv.Assignee.ID {
			return true, "reviewer-agent"
		}
		if c.Author.Kind == ticketing.ActorHuman && c.Author.ID != "" && c.Author.ID != "paperclip-user:local-board" {
			return true, "human:" + c.Author.ID
		}
		seenOther = true
	}
	if seenOther {
		return false, "PASS is not attributed to the assigned reviewer"
	}
	return false, "no PASS verdict"
}

// Approved continues a parent that waits for a human approval: called by the
// daemon after `dockweave gate approve <key>`; the Lead's next heartbeat
// closes it.
func (d *Dispatcher) Approved(ctx context.Context, issueKey string) error {
	d.init()
	ref, err := ticketing.ParseRef(issueKey, "paperclip")
	if err != nil || ref.Provider != d.Tickets.Name() {
		return nil // not a ticket of this provider
	}
	tk, err := d.Tickets.Tickets().Get(ctx, ref.ID)
	if err != nil || tk.Status != ticketing.StatusInReview || tk.Assignee == nil || tk.Assignee.Kind != ticketing.ActorAgent {
		return err
	}
	st := ticketing.StatusInProgress
	if _, err := d.Tickets.Tickets().Update(ctx, ref.ID, ticketing.Update{Status: &st}, ticketing.Actor{Kind: ticketing.ActorSystem}); err != nil {
		return err
	}
	return d.wake(ctx, tk.Assignee.ID, "Menschliche Freigabe erteilt")
}
