package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ananta888/dockweave/internal/dispatch"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/native"
	"github.com/ananta888/dockweave/internal/ticketing/paperclip"
	"github.com/ananta888/dockweave/internal/ticketing/paperclip/paperclipfake"
)

type countingSessions struct {
	mu      sync.Mutex
	prompts []string
	out     string
}

func (c *countingSessions) Prompt(_ context.Context, _, prompt string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts = append(c.prompts, prompt)
	return c.out, nil
}
func (c *countingSessions) Close(string) {}
func (c *countingSessions) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.prompts)
}

type passGates struct{ calls []dispatch.GateRequest }

func (g *passGates) Run(_ context.Context, r dispatch.GateRequest) (*model.GateRun, error) {
	g.calls = append(g.calls, r)
	return &model.GateRun{ID: "run-" + strings.Join(r.Phases, "+"), Status: model.GatePassed}, nil
}

type storeDir struct{ f *pipelineFixture }

func (d storeDir) Resolve(ctx context.Context, nameOrID string) (string, bool) {
	a, err := d.f.srv.Agents.Get(ctx, nameOrID)
	if err != nil {
		return "", false
	}
	return a.ID, true
}

// The same scenario gives the same result for the native provider and for
// Paperclip (ADR 0003: one code path for agents regardless of the backend).
func TestDispatcherEndToEndSameForBothProviders(t *testing.T) {
	for _, name := range []string{"native", "paperclip"} {
		t.Run(name, func(t *testing.T) {
			f := newPipelineFixture(t)
			sess := &countingSessions{out: "implemented\nSTATUS: DONE"}
			f.srv.Sessions = sess
			var p ticketing.Provider
			if name == "native" {
				p = native.New(f.store)
			} else {
				fake := paperclipfake.New(t, "ag_a", "ag_b")
				p = paperclip.New(paperclip.Options{BaseURL: fake.Srv.URL, Agents: storeDir{f}})
			}
			p = ticketing.Observe(p, NewTicketAudit(f.store))
			f.srv.Tickets = &ticketing.Selection{Primary: p}
			gates := &passGates{}
			d := f.srv.NewDispatcher(DispatchSettings{DefaultWorkspace: f.ws}, p, nil)
			d.Gates = gates
			f.srv.Dispatcher = d

			run := func(agent string) dispatch.Outcome {
				rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/run", `{"agent":"`+agent+`"}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("run: %d %s", rec.Code, rec.Body)
				}
				var out dispatch.Outcome
				_ = json.Unmarshal(rec.Body.Bytes(), &out)
				return out
			}
			if out := run("beta"); out.Kind != dispatch.OutcomeIdle {
				t.Fatalf("idle: %+v", out)
			}
			tk, err := p.Tickets().Create(context.Background(), ticketing.NewTicket{Title: "Fix login",
				Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_b"}}, ticketing.Actor{Kind: ticketing.ActorSystem})
			if err != nil {
				t.Fatal(err)
			}

			// Gated without a passing run: blocked, no turn spent.
			if r := f.pushEdges(t, 1); r.Code != http.StatusOK {
				t.Fatal(r.Body)
			}
			if out := run("beta"); out.Kind != dispatch.OutcomeBlocked || sess.n() != 0 {
				t.Fatalf("blocked: %+v prompts=%d", out, sess.n())
			}

			// After a PASS on the current content the agent works; DONE is
			// accepted after the gates and the answer is on the ticket.
			f.saveRun(t, "r-pass", model.GatePassed, "sha256:one")
			out := run("beta")
			if out.Kind != dispatch.OutcomeRan || out.Status != ticketing.StatusDone || sess.n() != 1 {
				t.Fatalf("ran: %+v prompts=%d", out, sess.n())
			}
			if !strings.Contains(sess.prompts[0], "Fix login") || !strings.Contains(sess.prompts[0], "STATUS: DONE") {
				t.Fatalf("prompt: %s", sess.prompts[0])
			}
			got, _ := p.Tickets().Get(context.Background(), tk.Ref.ID)
			if got.Status != ticketing.StatusDone {
				t.Fatalf("status: %s", got.Status)
			}
			cs, _ := p.Threads().Comments(context.Background(), tk.Ref.ID)
			last := cs[len(cs)-1]
			if !strings.Contains(last.Body, "implemented") || last.Author.Kind != ticketing.ActorAgent || last.Author.ID != "ag_b" {
				t.Fatalf("answer comment: %+v", last)
			}
			if len(gates.calls) < 2 || gates.calls[len(gates.calls)-1].UpstreamAgentID != "ag_b" || gates.calls[len(gates.calls)-1].IssueID != dispatch.IssueKey(tk.Ref) {
				t.Fatalf("gates: %+v", gates.calls)
			}
			// The blocked heartbeat and the turn are in the audit chain.
			evs, _ := f.store.ListEvents(context.Background(), 200)
			seen := map[string]bool{}
			for _, e := range evs {
				seen[string(e.Type)] = true
			}
			for _, typ := range []string{ticketing.EventCommentAdded, ticketing.EventStatusChanged} {
				if !seen[typ] {
					t.Fatalf("audit %s missing", typ)
				}
			}
		})
	}
}

func TestDispatchEndpointsNeedConfigurationAndPermission(t *testing.T) {
	f := newPipelineFixture(t)
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/run", `{"agent":"beta"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured: %d", rec.Code)
	}
	f.srv.Tickets = &ticketing.Selection{Primary: native.New(f.store)}
	f.srv.Dispatcher = f.srv.NewDispatcher(DispatchSettings{}, f.srv.Tickets.Primary, nil)
	if rec := f.do(t, RoleAdmin, http.MethodPost, "/api/v1/tickets/run", `{"agent":"ghost"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown agent: %d", rec.Code)
	}
	if rec := asHuman(t, f, []string{"dockweave-monitor"}, http.MethodPost, "/api/v1/tickets/run", `{"agent":"beta"}`); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "agent.wake") {
		t.Fatalf("permission: %d %s", rec.Code, rec.Body)
	}
}
