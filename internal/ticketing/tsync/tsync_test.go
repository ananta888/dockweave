package tsync

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
	"github.com/ananta888/dockweave/internal/ticketing/memory"
)

type audit struct {
	mu  sync.Mutex
	evs []ticketing.Event
}

func (a *audit) Publish(_ context.Context, e ticketing.Event) {
	a.mu.Lock()
	a.evs = append(a.evs, e)
	a.mu.Unlock()
}

type env struct {
	s     *Syncer
	p, m  ticketing.Provider
	store *persistence.Store
	aud   *audit
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "dw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e := &env{p: memory.New("native"), m: memory.New("paperclip"), store: store, aud: &audit{}}
	e.s = &Syncer{Primary: e.p, Mirror: e.m, Links: store, Marks: store, Audit: e.aud}
	return e
}

var alice = ticketing.Actor{Kind: ticketing.ActorHuman, ID: "iss|alice", Name: "alice"}

func (e *env) pass(t *testing.T, full bool) Report {
	t.Helper()
	rep, err := e.s.Pass(context.Background(), full, false)
	if err != nil {
		t.Fatalf("pass: %v %+v", err, rep.Errors)
	}
	return rep
}

func mustCreate(t *testing.T, p ticketing.Provider, n ticketing.NewTicket) *ticketing.Ticket {
	t.Helper()
	tk, err := p.Tickets().Create(context.Background(), n, alice)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func list(t *testing.T, p ticketing.Provider) []ticketing.Ticket {
	t.Helper()
	l, err := p.Tickets().List(context.Background(), ticketing.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (e *env) mirrorOf(t *testing.T, primary ticketing.Ref) *ticketing.Ticket {
	t.Helper()
	l, err := e.store.LinkByPrimary(context.Background(), primary.String())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := ticketing.ParseRef(l.MirrorRef, "")
	tk, err := e.m.Tickets().Get(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// Initial import: existing mirror tickets (Paperclip) with children,
// blockers and comments appear in the primary; a second pass changes nothing.
func TestInitialImportIsCompleteAndIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	parent := mustCreate(t, e.m, ticketing.NewTicket{Title: "Epic", Priority: ticketing.PriorityHigh})
	dep := mustCreate(t, e.m, ticketing.NewTicket{Title: "API", Parent: &parent.Ref, Assignee: &ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_b"}})
	child := mustCreate(t, e.m, ticketing.NewTicket{Title: "UI", Parent: &parent.Ref, BlockedBy: []ticketing.Ref{dep.Ref}})
	if _, err := e.m.Threads().AddComment(ctx, dep.Ref.ID, "started", ticketing.Actor{Kind: ticketing.ActorAgent, ID: "ag_b"}); err != nil {
		t.Fatal(err)
	}
	dry, err := e.s.Pass(ctx, true, true)
	if err != nil || dry.CreatedOnPrim != 3 || len(list(t, e.p)) != 0 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	rep := e.pass(t, true)
	if rep.CreatedOnPrim != 3 || rep.Comments != 1 {
		t.Fatalf("import: %+v", rep)
	}
	prim := list(t, e.p)
	if len(prim) != 3 {
		t.Fatalf("primary tickets: %d", len(prim))
	}
	byTitle := map[string]ticketing.Ticket{}
	for _, tk := range prim {
		byTitle[tk.Title] = tk
	}
	ui, api, epic := byTitle["UI"], byTitle["API"], byTitle["Epic"]
	if ui.Parent == nil || *ui.Parent != epic.Ref || len(ui.BlockedBy) != 1 || ui.BlockedBy[0] != api.Ref {
		t.Fatalf("structure not translated: %+v", ui)
	}
	if api.Assignee == nil || api.Assignee.ID != "ag_b" || epic.Priority != ticketing.PriorityHigh {
		t.Fatalf("fields: %+v %+v", api, epic)
	}
	cs, _ := e.p.Threads().Comments(ctx, api.Ref.ID)
	if len(cs) != 1 || cs[0].Body != "started" || cs[0].Author.ID != "ag_b" || !strings.HasPrefix(cs[0].Origin, "paperclip:") {
		t.Fatalf("comment: %+v", cs)
	}
	again := e.pass(t, true)
	if again.CreatedOnPrim+again.CreatedOnMirr+again.Updated+again.Comments != 0 || len(list(t, e.p)) != 3 || len(list(t, e.m)) != 3 {
		t.Fatalf("second pass changed something: %+v", again)
	}
	_ = child
}

// Changes on either side reach the other side within one pass.
func TestChangesFlowBothWays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := mustCreate(t, e.p, ticketing.NewTicket{Title: "Native ticket"})
	e.pass(t, true)
	mt := e.mirrorOf(t, tk.Ref)
	if mt.Title != "Native ticket" {
		t.Fatalf("mirror: %+v", mt)
	}
	st := ticketing.StatusInProgress
	if _, err := e.p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Status: &st}, alice); err != nil {
		t.Fatal(err)
	}
	title := "Renamed in Paperclip"
	if _, err := e.m.Tickets().Update(ctx, mt.Ref.ID, ticketing.Update{Title: &title}, alice); err != nil {
		t.Fatal(err)
	}
	e.pass(t, false)
	p, _ := e.p.Tickets().Get(ctx, tk.Ref.ID)
	m, _ := e.m.Tickets().Get(ctx, mt.Ref.ID)
	if p.Title != title || m.Status != st || p.Status != st || m.Title != title {
		t.Fatalf("merge: primary %+v mirror %+v", p, m)
	}
	// Comments in both directions, never echoed back.
	_, _ = e.p.Threads().AddComment(ctx, tk.Ref.ID, "from native", alice)
	_, _ = e.m.Threads().AddComment(ctx, mt.Ref.ID, "from paperclip", alice)
	e.pass(t, true)
	e.pass(t, true)
	pc, _ := e.p.Threads().Comments(ctx, tk.Ref.ID)
	mc, _ := e.m.Threads().Comments(ctx, mt.Ref.ID)
	if len(pc) != 2 || len(mc) != 2 {
		t.Fatalf("comments: primary %d mirror %d", len(pc), len(mc))
	}
}

// Both sides changed the same field: the newer change wins and the
// conflict is audited with both values.
func TestConflictNewerWinsAndIsAudited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := mustCreate(t, e.p, ticketing.NewTicket{Title: "x"})
	e.pass(t, true)
	mt := e.mirrorOf(t, tk.Ref)
	a, b := "primary title", "mirror title"
	if _, err := e.p.Tickets().Update(ctx, tk.Ref.ID, ticketing.Update{Title: &a}, alice); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := e.m.Tickets().Update(ctx, mt.Ref.ID, ticketing.Update{Title: &b}, alice); err != nil {
		t.Fatal(err)
	}
	rep := e.pass(t, false)
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].Winner != "mirror" || rep.Conflicts[0].PValue != a || rep.Conflicts[0].MValue != b {
		t.Fatalf("conflict: %+v", rep.Conflicts)
	}
	p, _ := e.p.Tickets().Get(ctx, tk.Ref.ID)
	if p.Title != b {
		t.Fatalf("winner not applied: %s", p.Title)
	}
	found := false
	for _, ev := range e.aud.evs {
		if ev.Type == "ticket.sync_conflict" && ev.Detail["primary_value"] == a && ev.Detail["mirror_value"] == b {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict not audited: %+v", e.aud.evs)
	}
	if st := e.s.Status(ctx); st.ConflictsAll != 1 || st.Linked != 1 || st.LastSuccess.IsZero() {
		t.Fatalf("status: %+v", st)
	}
}

// A comment-only change (no updated_at move) still syncs via the outbox.
func TestOutboxSyncsCommentOnlyChanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	obsPrimary := ticketing.Observe(e.p, e.s)
	e.s.Primary = obsPrimary
	tk := mustCreate(t, obsPrimary, ticketing.NewTicket{Title: "x"})
	e.pass(t, true)
	// Make the time filter miss the ticket.
	e.s.status.LastSuccess = time.Now().Add(time.Hour)
	if _, err := obsPrimary.Threads().AddComment(ctx, tk.Ref.ID, "late note", alice); err != nil {
		t.Fatal(err)
	}
	e.pass(t, false)
	mc, _ := e.m.Threads().Comments(ctx, e.mirrorOf(t, tk.Ref).Ref.ID)
	if len(mc) != 1 || mc[0].Body != "late note" {
		t.Fatalf("outbox comment: %+v", mc)
	}
}

// The mirror being down never blocks the primary; the pass reports it.
type down struct{ ticketing.Provider }

func (down) Tickets() ticketing.Tickets { return downTickets{} }

type downTickets struct{ ticketing.Tickets }

func (downTickets) List(context.Context, ticketing.Filter) ([]ticketing.Ticket, error) {
	return nil, ticketing.ErrUnavailable
}

func TestMirrorDownIsReportedNotFatal(t *testing.T) {
	e := newEnv(t)
	e.s.Mirror = down{e.m}
	rep, err := e.s.Pass(context.Background(), false, false)
	if err == nil || len(rep.Errors) == 0 {
		t.Fatalf("down mirror: %+v %v", rep, err)
	}
	if _, err := e.p.Tickets().Create(context.Background(), ticketing.NewTicket{Title: "still works"}, alice); err != nil {
		t.Fatal(err)
	}
}

// A ticket deleted on one side is unlinked and reported, never deleted on
// the other side.
func TestDeletedTicketIsUnlinkedNotPropagated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tk := mustCreate(t, e.p, ticketing.NewTicket{Title: "x"})
	e.pass(t, true)
	mt := e.mirrorOf(t, tk.Ref)
	if err := e.p.Tickets().(ticketing.Deleter).Delete(ctx, tk.Ref.ID, alice); err != nil {
		t.Fatal(err)
	}
	rep := e.pass(t, true)
	if len(rep.Unlinked) != 1 {
		t.Fatalf("unlinked: %+v", rep)
	}
	if _, err := e.m.Tickets().Get(ctx, mt.Ref.ID); err != nil {
		t.Fatalf("mirror copy was deleted: %v", err)
	}
	// The deletion does not come back: no re-import, reported only once.
	if rep := e.pass(t, true); rep.CreatedOnPrim != 0 || len(rep.Unlinked) != 0 {
		t.Fatalf("after tombstone: %+v", rep)
	}
	if n := len(list(t, e.p)); n != 0 {
		t.Fatalf("deleted ticket came back: %d", n)
	}
}
