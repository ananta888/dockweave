// Package tsync keeps a mirror ticket provider in sync with the primary
// (ADR 0003 D-8): links between tickets, field-level three-way merge against
// the last synced state (the newer change wins, a tie goes to the primary),
// append-only comment merge by origin, every conflict audited. Sync failures
// never block the primary; agents are only driven from the primary.
package tsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
	"github.com/ananta888/dockweave/internal/ticketing"
)

// Links persists ticket links (persistence.Store).
type Links interface {
	LinkByPrimary(ctx context.Context, ref string) (*persistence.TicketLink, error)
	LinkByMirror(ctx context.Context, ref string) (*persistence.TicketLink, error)
	PutTicketLink(ctx context.Context, l persistence.TicketLink) error
	CountTicketLinks(ctx context.Context) (int, error)
}

// Marks are idempotency markers (persistence.Store dispatch_actions).
type Marks interface {
	GetDispatchAction(ctx context.Context, key string) (model.DispatchAction, error)
	PutDispatchAction(ctx context.Context, a model.DispatchAction) error
}

// Conflict is one field changed on both sides since the last sync.
type Conflict struct {
	Primary string `json:"primary"`
	Mirror  string `json:"mirror"`
	Field   string `json:"field"`
	PValue  string `json:"primary_value"`
	MValue  string `json:"mirror_value"`
	Winner  string `json:"winner"` // primary | mirror
}

// Report is the result of one pass.
type Report struct {
	DryRun        bool       `json:"dry_run"`
	Full          bool       `json:"full"`
	CreatedOnPrim int        `json:"created_on_primary"`
	CreatedOnMirr int        `json:"created_on_mirror"`
	Updated       int        `json:"updated"`
	Unlinked      []string   `json:"unlinked,omitempty"` // deleted on one side; never propagated
	Comments      int        `json:"comments"`
	Conflicts     []Conflict `json:"conflicts,omitempty"`
	Errors        []string   `json:"errors,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    time.Time  `json:"finished_at"`
}

// Status is the public sync state (GET /api/v1/tickets/sync).
type Status struct {
	Primary      string    `json:"primary"`
	Mirror       string    `json:"mirror"`
	Linked       int       `json:"linked"`
	LastPass     time.Time `json:"last_pass,omitempty"`
	LastSuccess  time.Time `json:"last_success,omitempty"`
	LagSeconds   int       `json:"lag_seconds"`
	ConflictsAll int       `json:"conflicts_total"`
	LastReport   *Report   `json:"last_report,omitempty"`
}

// Syncer mirrors tickets between Primary and Mirror.
type Syncer struct {
	Primary  ticketing.Provider
	Mirror   ticketing.Provider
	Links    Links
	Marks    Marks
	Audit    ticketing.Publisher
	Interval time.Duration // reconcile pass, default 30 s
	Now      func() time.Time

	passMu  sync.Mutex // one pass at a time
	mu      sync.Mutex
	status  Status
	kick    chan struct{}
	once    sync.Once
	pending map[string]ticketing.Ref // outbox: refs changed since the last pass
}

func (s *Syncer) init() {
	s.once.Do(func() {
		if s.Interval <= 0 {
			s.Interval = 30 * time.Second
		}
		if s.Now == nil {
			s.Now = time.Now
		}
		s.kick = make(chan struct{}, 1)
		s.status.Primary, s.status.Mirror = s.Primary.Name(), s.Mirror.Name()
	})
}

// Publish implements ticketing.Publisher (outbox): a ticket change on either
// side triggers a prompt incremental pass.
func (s *Syncer) Publish(_ context.Context, e ticketing.Event) {
	s.init()
	if e.Actor == syncActor {
		return // our own writes need no further pass
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]ticketing.Ref{}
	}
	s.pending[e.Ticket.String()] = e.Ticket
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run reconciles every Interval and on Publish until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	s.init()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
			// Coalesce bursts of events into one pass.
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
		_, _ = s.Pass(ctx, false, false)
	}
}

// Status returns the current sync state.
func (s *Syncer) Status(ctx context.Context) Status {
	s.init()
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	if n, err := s.Links.CountTicketLinks(ctx); err == nil {
		st.Linked = n
	}
	if !st.LastSuccess.IsZero() {
		st.LagSeconds = int(s.Now().Sub(st.LastSuccess).Seconds())
	}
	return st
}

// fields is the canonical, comparable state of a ticket.
type fields struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	Assignee    string `json:"assignee"` // "kind:id" or ""
}

func fieldsOf(t *ticketing.Ticket) fields {
	f := fields{Title: t.Title, Description: t.Description, Status: string(t.Status), Priority: string(t.Priority)}
	if t.Assignee != nil {
		f.Assignee = string(t.Assignee.Kind) + ":" + t.Assignee.ID
	}
	return f
}

func (f fields) get(name string) string {
	switch name {
	case "title":
		return f.Title
	case "description":
		return f.Description
	case "status":
		return f.Status
	case "priority":
		return f.Priority
	}
	return f.Assignee
}

func (f *fields) set(name, v string) {
	switch name {
	case "title":
		f.Title = v
	case "description":
		f.Description = v
	case "status":
		f.Status = v
	case "priority":
		f.Priority = v
	default:
		f.Assignee = v
	}
}

var fieldNames = []string{"title", "description", "status", "priority", "assignee"}

func updateFor(name, v string, u *ticketing.Update) {
	switch name {
	case "title":
		u.Title = &v
	case "description":
		u.Description = &v
	case "status":
		st := ticketing.Status(v)
		u.Status = &st
	case "priority":
		if v != "" {
			p := ticketing.Priority(v)
			u.Priority = &p
		}
	case "assignee":
		if v == "" {
			u.ClearAssignee = true
			return
		}
		kind, id, _ := strings.Cut(v, ":")
		u.Assignee = &ticketing.Actor{Kind: ticketing.ActorKind(kind), ID: id}
	}
}

var syncActor = ticketing.Actor{Kind: ticketing.ActorSystem, ID: "sync", Name: "DockWeave sync"}

// Pass runs one reconcile pass. full=true considers every ticket (initial
// import); otherwise only tickets changed since the last successful pass.
// dryRun plans without writing anything.
func (s *Syncer) Pass(ctx context.Context, full, dryRun bool) (Report, error) {
	s.init()
	s.passMu.Lock()
	defer s.passMu.Unlock()
	rep := Report{DryRun: dryRun, Full: full, StartedAt: s.Now().UTC()}
	var since time.Time
	s.mu.Lock()
	if !full && !s.status.LastSuccess.IsZero() {
		since = s.status.LastSuccess.Add(-2 * s.Interval) // clock skew between systems
	}
	s.mu.Unlock()

	plist, perr := s.Primary.Tickets().List(ctx, ticketing.Filter{UpdatedSince: since})
	mlist, merr := s.Mirror.Tickets().List(ctx, ticketing.Filter{UpdatedSince: since})
	if perr != nil || merr != nil {
		err := errors.Join(perr, merr)
		rep.Errors = append(rep.Errors, err.Error())
		s.finish(rep, false)
		return rep, err
	}
	sortByCreated(plist)
	sortByCreated(mlist)
	done := map[string]bool{}
	for i := range plist {
		p := plist[i]
		l, err := s.Links.LinkByPrimary(ctx, p.Ref.String())
		switch {
		case errors.Is(err, persistence.ErrNotFound):
			s.create(ctx, &p, s.Mirror, true, &rep)
		case err != nil:
			rep.Errors = append(rep.Errors, err.Error())
		default:
			s.pair(ctx, &p, nil, l, &rep)
			done[l.PrimaryRef] = true
		}
	}
	for i := range mlist {
		m := mlist[i]
		l, err := s.Links.LinkByMirror(ctx, m.Ref.String())
		switch {
		case errors.Is(err, persistence.ErrNotFound):
			s.create(ctx, &m, s.Primary, false, &rep)
		case err != nil:
			rep.Errors = append(rep.Errors, err.Error())
		case !done[l.PrimaryRef]:
			done[l.PrimaryRef] = true
			s.pair(ctx, nil, &m, l, &rep)
		}
	}
	// Outbox: tickets with events (e.g. a new comment, which does not move
	// updated_at) are merged even when the time filter missed them.
	s.mu.Lock()
	queued := s.pending
	if !dryRun {
		s.pending = nil
	}
	s.mu.Unlock()
	for key, ref := range queued {
		var l *persistence.TicketLink
		var err error
		switch ref.Provider {
		case s.Primary.Name():
			l, err = s.Links.LinkByPrimary(ctx, key)
		case s.Mirror.Name():
			l, err = s.Links.LinkByMirror(ctx, key)
		default:
			continue
		}
		if err != nil || done[l.PrimaryRef] {
			continue
		}
		done[l.PrimaryRef] = true
		s.pair(ctx, nil, nil, l, &rep)
	}
	rep.FinishedAt = s.Now().UTC()
	s.finish(rep, len(rep.Errors) == 0)
	if len(rep.Errors) > 0 {
		return rep, fmt.Errorf("sync incomplete: %d error(s)", len(rep.Errors))
	}
	return rep, nil
}

func (s *Syncer) finish(rep Report, ok bool) {
	if rep.DryRun {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastPass = rep.StartedAt
	if ok {
		s.status.LastSuccess = rep.StartedAt
	}
	s.status.ConflictsAll += len(rep.Conflicts)
	r := rep
	s.status.LastReport = &r
}

func sortByCreated(l []ticketing.Ticket) {
	sort.SliceStable(l, func(i, j int) bool { return l[i].CreatedAt.Before(l[j].CreatedAt) })
}

// translate maps a ref of one side to the linked ref of the other side.
func (s *Syncer) translate(ctx context.Context, r ticketing.Ref, toMirror bool) (ticketing.Ref, bool) {
	var l *persistence.TicketLink
	var err error
	if toMirror {
		l, err = s.Links.LinkByPrimary(ctx, r.String())
	} else {
		l, err = s.Links.LinkByMirror(ctx, r.String())
	}
	if err != nil {
		return ticketing.Ref{}, false
	}
	target := l.MirrorRef
	if !toMirror {
		target = l.PrimaryRef
	}
	out, err := ticketing.ParseRef(target, "")
	return out, err == nil
}

// create copies src to the other side and links both.
func (s *Syncer) create(ctx context.Context, src *ticketing.Ticket, dst ticketing.Provider, toMirror bool, rep *Report) {
	if rep.DryRun {
		if toMirror {
			rep.CreatedOnMirr++
		} else {
			rep.CreatedOnPrim++
		}
		return
	}
	n := ticketing.NewTicket{Title: src.Title, Description: src.Description, Status: src.Status, Priority: src.Priority,
		WorkspaceID: src.WorkspaceID}
	if src.Assignee != nil && src.Assignee.Kind != ticketing.ActorSystem {
		a := *src.Assignee
		n.Assignee = &a
	}
	if src.Parent != nil {
		if r, ok := s.translate(ctx, *src.Parent, toMirror); ok {
			n.Parent = &r
		}
	}
	for _, b := range src.BlockedBy {
		if r, ok := s.translate(ctx, b, toMirror); ok {
			n.BlockedBy = append(n.BlockedBy, r)
		}
	}
	if dst.Name() != "native" {
		n.WorkspaceID = "" // DockWeave workspaces mean nothing elsewhere
	}
	by := syncActor
	if src.CreatedBy != nil {
		by = *src.CreatedBy
	}
	created, err := dst.Tickets().Create(ctx, n, by)
	if errors.Is(err, ticketing.ErrInvalid) && n.Assignee != nil {
		// The assignee cannot be represented there (e.g. a DockWeave human
		// in Paperclip): create unassigned, keep the field for the merge.
		n.Assignee = nil
		created, err = dst.Tickets().Create(ctx, n, by)
	}
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("create %s on %s: %v", src.Ref, dst.Name(), err))
		return
	}
	l := persistence.TicketLink{PrimaryRef: src.Ref.String(), MirrorRef: created.Ref.String()}
	if !toMirror {
		l = persistence.TicketLink{PrimaryRef: created.Ref.String(), MirrorRef: src.Ref.String()}
	}
	b, _ := json.Marshal(fieldsOf(src))
	l.Base = string(b)
	if err := s.Links.PutTicketLink(ctx, l); err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	if toMirror {
		rep.CreatedOnMirr++
	} else {
		rep.CreatedOnPrim++
	}
	p, m := src, created
	if !toMirror {
		p, m = created, src
	}
	s.comments(ctx, p, m, rep)
}

// pair merges a linked ticket pair (either side may be given; the other is
// loaded).
// tombstone marks a link whose ticket was deleted on one side.
const tombstone = `{"deleted":true}`

func (s *Syncer) pair(ctx context.Context, p, m *ticketing.Ticket, l *persistence.TicketLink, rep *Report) {
	if l.Base == tombstone {
		return
	}
	gone := false
	load := func(prov ticketing.Provider, ref string) *ticketing.Ticket {
		r, err := ticketing.ParseRef(ref, "")
		if err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			return nil
		}
		t, err := prov.Tickets().Get(ctx, r.ID)
		if errors.Is(err, ticketing.ErrNotFound) {
			gone = true
			return nil
		}
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("load %s: %v", ref, err))
			return nil
		}
		return t
	}
	if p == nil {
		p = load(s.Primary, l.PrimaryRef)
	}
	if m == nil {
		m = load(s.Mirror, l.MirrorRef)
	}
	if gone {
		// Deleted on one side: the link becomes a tombstone, reported once.
		// A deletion is never propagated, and the remaining copy is never
		// imported back (the delete would otherwise "come back").
		rep.Unlinked = append(rep.Unlinked, l.PrimaryRef+" <-> "+l.MirrorRef)
		if !rep.DryRun {
			l.Base = tombstone
			if err := s.Links.PutTicketLink(ctx, *l); err != nil {
				rep.Errors = append(rep.Errors, err.Error())
			}
		}
		return
	}
	if p == nil || m == nil {
		return
	}
	var base fields
	_ = json.Unmarshal([]byte(l.Base), &base)
	fp, fm := fieldsOf(p), fieldsOf(m)
	merged := base
	var up, um ticketing.Update
	changedP, changedM := false, false
	for _, f := range fieldNames {
		bv, pv, mv := base.get(f), fp.get(f), fm.get(f)
		pc, mc := pv != bv, mv != bv
		switch {
		case pc && !mc:
			updateFor(f, pv, &um)
			changedM = true
			merged.set(f, pv)
		case mc && !pc:
			updateFor(f, mv, &up)
			changedP = true
			merged.set(f, mv)
		case pc && mc && pv != mv:
			winner := "primary"
			if m.UpdatedAt.After(p.UpdatedAt) {
				winner = "mirror"
			}
			c := Conflict{Primary: l.PrimaryRef, Mirror: l.MirrorRef, Field: f, PValue: pv, MValue: mv, Winner: winner}
			rep.Conflicts = append(rep.Conflicts, c)
			if winner == "primary" {
				updateFor(f, pv, &um)
				changedM = true
				merged.set(f, pv)
			} else {
				updateFor(f, mv, &up)
				changedP = true
				merged.set(f, mv)
			}
			if !rep.DryRun && s.Audit != nil {
				s.Audit.Publish(ctx, ticketing.Event{Type: "ticket.sync_conflict", Ticket: p.Ref, Actor: syncActor, At: s.Now().UTC(),
					Detail: map[string]any{"mirror": l.MirrorRef, "field": f, "primary_value": pv, "mirror_value": mv, "winner": winner}})
			}
		case pc && mc:
			merged.set(f, pv) // both changed to the same value
		}
	}
	if rep.DryRun {
		if changedP || changedM {
			rep.Updated++
		}
		return
	}
	if changedM {
		if _, err := s.Mirror.Tickets().Update(ctx, m.Ref.ID, um, syncActor); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("update %s: %v", m.Ref, err))
			if !errors.Is(err, ticketing.ErrInvalid) {
				return // retried next pass; an unrepresentable value is not retried forever
			}
		}
	}
	if changedP {
		if _, err := s.Primary.Tickets().Update(ctx, p.Ref.ID, up, syncActor); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("update %s: %v", p.Ref, err))
			if !errors.Is(err, ticketing.ErrInvalid) {
				return
			}
		}
	}
	if changedP || changedM {
		rep.Updated++
	}
	if merged != base {
		b, _ := json.Marshal(merged)
		l.Base = string(b)
		if err := s.Links.PutTicketLink(ctx, *l); err != nil {
			rep.Errors = append(rep.Errors, err.Error())
		}
	}
	s.comments(ctx, p, m, rep)
}

// comments appends comments missing on either side, keeping their origin;
// a comment that came from the target side is never copied back.
func (s *Syncer) comments(ctx context.Context, p, m *ticketing.Ticket, rep *Report) {
	if rep.DryRun || s.Primary.Threads() == nil || s.Mirror.Threads() == nil {
		return
	}
	cp, err1 := s.Primary.Threads().Comments(ctx, p.Ref.ID)
	cm, err2 := s.Mirror.Threads().Comments(ctx, m.Ref.ID)
	if err1 != nil || err2 != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("comments %s: %v", p.Ref, errors.Join(err1, err2)))
		return
	}
	s.copyComments(ctx, cp, cm, s.Mirror, m.Ref, rep)
	s.copyComments(ctx, cm, cp, s.Primary, p.Ref, rep)
}

func (s *Syncer) copyComments(ctx context.Context, from, to []ticketing.Comment, dst ticketing.Provider, dstRef ticketing.Ref, rep *Report) {
	have := map[string]bool{}
	for _, c := range to {
		have[c.Origin] = true
	}
	oc, ok := dst.Threads().(ticketing.OriginCommenter)
	if !ok {
		return
	}
	for _, c := range from {
		origin := c.Origin
		if origin == "" || have[origin] || strings.HasPrefix(origin, dst.Name()+":") {
			continue
		}
		key := "sync-comment|" + dstRef.String() + "|" + origin
		st, err := s.Marks.GetDispatchAction(ctx, key)
		if err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			continue
		}
		if st.Status == "done" || (st.Status == "pending" && s.Now().Sub(st.Since) < 10*time.Minute) {
			continue
		}
		_ = s.Marks.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "pending", Since: s.Now()})
		if _, err := oc.AddCommentWithOrigin(ctx, dstRef.ID, c.Body, c.Author, origin); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("comment %s -> %s: %v", origin, dstRef, err))
			continue
		}
		_ = s.Marks.PutDispatchAction(ctx, model.DispatchAction{Key: key, Status: "done", Since: s.Now()})
		rep.Comments++
	}
}
