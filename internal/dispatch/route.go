package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// Route re-applies the pipeline routing for a ticket of upstream (retry of
// an incomplete routing); idempotent.
func (d *Dispatcher) Route(ctx context.Context, upstream string, tk *ticketing.Ticket) ([]string, error) {
	return d.route(ctx, upstream, tk, nil)
}

// noRework: codes whose failure is not the upstream agent's fixable work.
var noRework = map[string]bool{"tamper": true, "unavailable": true, "fingerprint_unavailable": true}

// route applies the pipeline after a gate run of upstream (cmd_route in
// pipeline.py): wake released downstream agents (allow / wake:<agent>), open
// exactly one rework ticket per gate run for `rework`, report `deny`. The
// returned lines are recorded on the ticket; an error means the routing is
// incomplete (retry is safe: every effect is idempotent).
func (d *Dispatcher) route(ctx context.Context, upstream string, tk *ticketing.Ticket, run *model.GateRun) ([]string, error) {
	d.init()
	if d.Pipeline == nil {
		return nil, nil
	}
	rev, edges, err := d.Pipeline.Edges(ctx)
	if err != nil {
		return []string{"ERR\tpipeline list failed: " + truncate(err.Error(), 200)}, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	up, err := d.admission(ctx, upstream)
	upID := up.AgentID
	if err != nil || upID == "" {
		return []string{"ERR\tupstream agent " + upstream + " unknown to the daemon"}, fmt.Errorf("upstream %s unknown", upstream)
	}
	var downs []string
	for _, e := range edges {
		if e.UpstreamAgentID == upID && !contains(downs, e.DownstreamAgentID) {
			downs = append(downs, e.DownstreamAgentID)
		}
	}
	if len(downs) == 0 {
		return nil, nil
	}
	runID := ""
	if run != nil {
		runID = run.ID
	}
	var lines []string
	failed := false
	for _, down := range downs {
		adm, aerr := d.admission(ctx, down)
		if aerr != nil {
			lines = append(lines, "ERR\tdownstream admission unavailable: "+down)
			failed = true
			continue
		}
		name := firstNonEmpty(adm.Agent, down)
		var mine []model.AdmissionEdge
		for _, e := range adm.Edges {
			if e.UpstreamAgentID == upID {
				mine = append(mine, e)
			}
		}
		if adm.Allowed {
			action := firstNonEmpty(adm.Action, "allow")
			target := down
			if strings.HasPrefix(action, "wake:") {
				target = strings.TrimPrefix(action, "wake:")
			}
			targetName := name
			if target != down {
				targetName = target
			}
			var ev []string
			for _, e := range mine {
				ev = append(ev, e.RunID)
			}
			sort.Strings(ev)
			evidence := strings.Join(ev, ",")
			if evidence == "" {
				evidence = runID
			}
			key := fmt.Sprintf("wake|%s|%s|%d|%s", upID, target, rev, evidence)
			_, werr := d.acts.once(ctx, key, func() (string, error) {
				return "", d.wake(ctx, target, "Pipeline frei: Gate nach "+upstream+" bestanden")
			})
			if werr != nil {
				failed = true
				lines = append(lines, fmt.Sprintf("ERR\t%s\t%s\t%v", targetName, action, werr))
				continue
			}
			lines = append(lines, fmt.Sprintf("WAKE\t%s\t%s", targetName, action))
			continue
		}
		for _, e := range mine {
			if e.Satisfied {
				continue
			}
			reason := firstNonEmpty(e.Reason, e.Message)
			if e.Action == "rework" && !noRework[e.Code] {
				ident, rerr := d.gateRework(ctx, upID, tk, e.GateID, firstNonEmpty(e.RunID, runID), reason)
				if rerr != nil {
					failed = true
					lines = append(lines, fmt.Sprintf("ERR\trework %s: %v", e.GateID, rerr))
					continue
				}
				lines = append(lines, fmt.Sprintf("REWORK\t%s\t%s", e.GateID, ident))
				continue
			}
			lines = append(lines, fmt.Sprintf("DENY\t%s\t%s\t%s\t%s", name, e.GateID, e.Code, reason))
		}
	}
	if failed {
		return lines, errors.New("routing incomplete")
	}
	return lines, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (d *Dispatcher) wake(ctx context.Context, agent, reason string) error {
	if d.Waker == nil {
		return errors.New("no waker configured")
	}
	return d.Waker.Wake(ctx, agent, reason)
}

// gateRework opens exactly one rework ticket per gate run for the upstream
// agent and wakes it once. An existing ticket carrying the run tag is
// reused; an unknown creation outcome is reconciled through that tag.
func (d *Dispatcher) gateRework(ctx context.Context, upstream string, tk *ticketing.Ticket, gate, runID, reason string) (string, error) {
	d.init()
	if gate == "" || runID == "" {
		return "", errors.New("gate or gate run missing")
	}
	tag := fmt.Sprintf("[gate %s / run %s]", gate, runID)
	key := fmt.Sprintf("rework|%s|%s|%s", upstream, gate, runID)
	ident, err := d.createOnce(ctx, key, tag, ticketing.NewTicket{
		Title:       "Rework: Gate " + gate + " " + tag,
		Description: fmt.Sprintf("Gate %s, run %s: %s", gate, runID, reason),
		Assignee:    &ticketing.Actor{Kind: ticketing.ActorAgent, ID: upstream},
		Parent:      tk.Parent,
		WorkspaceID: tk.WorkspaceID,
	})
	if err != nil {
		return "", err
	}
	if _, err := d.acts.once(ctx, key+"|wake", func() (string, error) {
		return "", d.wake(ctx, upstream, "Rework nach Gate "+gate)
	}); err != nil {
		return "", fmt.Errorf("ticket %s exists; wake failed: %w", ident, err)
	}
	return ident, nil
}

// createOnce creates a ticket at most once per key; tag (part of the title)
// identifies it when the marker is lost or the outcome was unknown.
func (d *Dispatcher) createOnce(ctx context.Context, key, tag string, n ticketing.NewTicket) (string, error) {
	defer d.acts.lock(key + "|create")()
	st, err := d.Log.GetDispatchAction(ctx, key)
	if err != nil {
		return "", err
	}
	if st.Status == "done" && st.Result != "" {
		return st.Result, nil
	}
	list, err := d.Tickets.Tickets().List(ctx, ticketing.Filter{Query: tag})
	if err != nil {
		return "", fmt.Errorf("cannot reconcile existing tickets: %w", err)
	}
	for _, t := range list {
		if strings.Contains(t.Title, tag) {
			ident := firstNonEmpty(t.Identifier, t.Ref.ID)
			return ident, d.Log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "done", Since: d.Config.Now(), Result: ident})
		}
	}
	if d.acts.pendingBlocks(st) {
		return "", errors.New("creation outcome unknown; reconcile before retry")
	}
	if err := d.Log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "pending", Since: d.Config.Now()}); err != nil {
		return "", err
	}
	created, err := d.Tickets.Tickets().Create(ctx, n, ticketing.Actor{Kind: ticketing.ActorSystem})
	if err != nil {
		if !unknownOutcome(err) {
			_ = d.Log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "retry", Since: d.Config.Now()})
		}
		return "", err
	}
	ident := firstNonEmpty(created.Identifier, created.Ref.ID)
	return ident, d.Log.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "done", Since: d.Config.Now(), Result: ident})
}

// reviewRework opens a rework ticket for the author of the reviewed work
// (the assignee of the first dependency: blocked_by, else DEPENDS_ON in the
// description; paperclip-gate.py rework) and wakes that agent.
func (d *Dispatcher) reviewRework(ctx context.Context, tk *ticketing.Ticket, reason string) string {
	var deps []string
	for _, b := range tk.BlockedBy {
		deps = append(deps, b.ID)
	}
	deps = append(deps, dependsOn(tk.Description)...)
	author := ""
	for _, dep := range deps {
		if src, err := d.Tickets.Tickets().Get(ctx, dep); err == nil && src.Assignee != nil && src.Assignee.Kind == ticketing.ActorAgent {
			author = src.Assignee.ID
			break
		}
	}
	if author == "" {
		return "kein Autor fuer die Nacharbeit gefunden (keine Abhaengigkeit mit Agent)"
	}
	h := sha256.Sum256([]byte(reason))
	tag := "[review " + tk.Ref.ID + " / " + hex.EncodeToString(h[:4]) + "]"
	ident, err := d.createOnce(ctx, "review-rework|"+tk.Ref.String()+"|"+strconv.Itoa(len(reason))+"|"+hex.EncodeToString(h[:8]), tag, ticketing.NewTicket{
		Title:       "Rework: " + tk.Title + " " + tag,
		Description: "Review-Ergebnis:\n\n" + strings.TrimSpace(reason) + "\n\nBitte nachbessern und erneut STATUS: DONE melden.",
		Assignee:    &ticketing.Actor{Kind: ticketing.ActorAgent, ID: author},
		Parent:      tk.Parent,
		WorkspaceID: tk.WorkspaceID,
	})
	if err != nil {
		return "Rework konnte nicht angelegt werden: " + err.Error()
	}
	if err := d.wake(ctx, author, "Rework nach FAIL"); err != nil {
		return ident + " (Agent " + author + " nicht geweckt: " + err.Error() + ")"
	}
	return ident + " → " + author
}
