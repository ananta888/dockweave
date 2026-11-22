// Package ticketing is the provider-neutral port for tickets (ADR 0003).
//
// The core (dispatch, API, UI) depends only on this package. Backends such as
// Paperclip or the native DockWeave store are adapters that implement the
// small interfaces below; a backend implements only what it can do and
// reports it through Capabilities (interface segregation). Adapter-specific
// data never leaks into the canonical types; unknown extras stay in
// Ticket.Extra.
package ticketing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the canonical ticket state. Adapters map their own states.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusInReview   Status = "in_review"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

// Statuses lists every canonical status in workflow order.
var Statuses = []Status{StatusBacklog, StatusTodo, StatusInProgress, StatusInReview, StatusBlocked, StatusDone, StatusCancelled}

// Valid reports whether s is a canonical status.
func (s Status) Valid() bool {
	for _, v := range Statuses {
		if s == v {
			return true
		}
	}
	return false
}

// Closed reports whether no further work is expected.
func (s Status) Closed() bool { return s == StatusDone || s == StatusCancelled }

// Priority is the canonical urgency.
type Priority string

const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityMedium   Priority = "medium"
	PriorityLow      Priority = "low"
)

// Valid reports whether p is a canonical priority (empty = unset).
func (p Priority) Valid() bool {
	switch p {
	case "", PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	}
	return false
}

// ActorKind tells who acted or is assigned.
type ActorKind string

const (
	ActorHuman  ActorKind = "human"
	ActorAgent  ActorKind = "agent"
	ActorSystem ActorKind = "system"
)

func (k ActorKind) String() string { return string(k) }

// Actor is a human (stable principal id, e.g. "iss|sub"), an agent (stable
// DockWeave agent id) or the system.
type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
	Name string    `json:"name,omitempty"`
}

// Valid reports whether the actor is usable as author or assignee.
func (a Actor) Valid() bool {
	switch a.Kind {
	case ActorHuman, ActorAgent:
		return strings.TrimSpace(a.ID) != ""
	case ActorSystem:
		return true
	}
	return false
}

// Ref identifies a ticket across providers: "<provider>:<id>".
type Ref struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

func (r Ref) String() string { return r.Provider + ":" + r.ID }

// IsZero reports an unset reference.
func (r Ref) IsZero() bool { return r.Provider == "" && r.ID == "" }

// ParseRef parses "<provider>:<id>". A bare id belongs to defaultProvider,
// which keeps legacy Paperclip issue ids valid (ADR 0003 D-1).
func ParseRef(s, defaultProvider string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("%w: empty ticket reference", ErrInvalid)
	}
	if p, id, ok := strings.Cut(s, ":"); ok && validProviderName(p) && id != "" && !strings.HasPrefix(id, "/") {
		return Ref{Provider: p, ID: id}, nil
	}
	if defaultProvider == "" {
		return Ref{}, fmt.Errorf("%w: ticket reference %q has no provider", ErrInvalid, s)
	}
	return Ref{Provider: defaultProvider, ID: s}, nil
}

// validProviderName: lower-case identifier ([a-z][a-z0-9_-]*).
func validProviderName(p string) bool {
	if p == "" || p[0] < 'a' || p[0] > 'z' {
		return false
	}
	for _, c := range p {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// Ticket is the canonical ticket.
type Ticket struct {
	Ref         Ref      `json:"ref"`
	Identifier  string   `json:"identifier,omitempty"` // human-friendly, e.g. "PC-12", "DW-7"
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Status      Status   `json:"status"`
	Priority    Priority `json:"priority,omitempty"`
	Assignee    *Actor   `json:"assignee,omitempty"`
	Parent      *Ref     `json:"parent,omitempty"`
	// BlockedBy lists tickets that must be closed before work may start.
	BlockedBy []Ref `json:"blocked_by,omitempty"`
	// WorkspaceID binds the ticket to a DockWeave workspace (optional).
	WorkspaceID string `json:"workspace_id,omitempty"`
	CreatedBy   *Actor `json:"created_by,omitempty"`
	// CheckedOutBy is the agent currently working on the ticket (Inbox).
	CheckedOutBy string    `json:"checked_out_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Version changes on every update; Update with ExpectedVersion fails
	// with ErrConflict when it no longer matches (optimistic locking).
	Version string `json:"version"`
	// Extra carries provider fields without a canonical meaning (read-only).
	Extra map[string]any `json:"extra,omitempty"`
}

// NewTicket is the input for Tickets.Create.
type NewTicket struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Status      Status   `json:"status,omitempty"` // empty = todo
	Priority    Priority `json:"priority,omitempty"`
	Assignee    *Actor   `json:"assignee,omitempty"`
	Parent      *Ref     `json:"parent,omitempty"`
	BlockedBy   []Ref    `json:"blocked_by,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
}

// Validate normalises and checks the input.
func (n *NewTicket) Validate() error {
	n.Title = strings.TrimSpace(n.Title)
	if n.Title == "" {
		return fmt.Errorf("%w: title must not be empty", ErrInvalid)
	}
	if n.Status == "" {
		n.Status = StatusTodo
	}
	if !n.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, n.Status)
	}
	if !n.Priority.Valid() {
		return fmt.Errorf("%w: unknown priority %q", ErrInvalid, n.Priority)
	}
	if n.Assignee != nil && (!n.Assignee.Valid() || n.Assignee.Kind == ActorSystem) {
		return fmt.Errorf("%w: assignee must be a human or an agent with an id", ErrInvalid)
	}
	return nil
}

// Update changes selected fields; nil means unchanged.
type Update struct {
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	Status      *Status   `json:"status,omitempty"`
	Priority    *Priority `json:"priority,omitempty"`
	// Assignee sets the assignee; ClearAssignee removes it.
	Assignee      *Actor `json:"assignee,omitempty"`
	ClearAssignee bool   `json:"clear_assignee,omitempty"`
	// BlockedBy replaces the blockers when non-nil (empty slice clears).
	BlockedBy       *[]Ref `json:"blocked_by,omitempty"`
	ExpectedVersion string `json:"expected_version,omitempty"`
}

// Validate checks the update.
func (u *Update) Validate() error {
	if u.Title != nil && strings.TrimSpace(*u.Title) == "" {
		return fmt.Errorf("%w: title must not be empty", ErrInvalid)
	}
	if u.Status != nil && !u.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, *u.Status)
	}
	if u.Priority != nil && !u.Priority.Valid() {
		return fmt.Errorf("%w: unknown priority %q", ErrInvalid, *u.Priority)
	}
	if u.Assignee != nil && (!u.Assignee.Valid() || u.Assignee.Kind == ActorSystem) {
		return fmt.Errorf("%w: assignee must be a human or an agent with an id", ErrInvalid)
	}
	if u.Assignee != nil && u.ClearAssignee {
		return fmt.Errorf("%w: assignee and clear_assignee are exclusive", ErrInvalid)
	}
	return nil
}

// Filter narrows Tickets.List; zero values mean "any".
type Filter struct {
	Status       []Status  `json:"status,omitempty"`
	Assignee     *Actor    `json:"assignee,omitempty"`
	Parent       *Ref      `json:"parent,omitempty"`
	UpdatedSince time.Time `json:"updated_since,omitempty"`
	Query        string    `json:"query,omitempty"`
	Limit        int       `json:"limit,omitempty"`
}

// Comment is one entry in a ticket thread. Threads are append-only.
type Comment struct {
	ID        string    `json:"id"`
	Ticket    Ref       `json:"ticket"`
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	// Origin is the id of the comment in the system it was first written in
	// (used by the live sync to merge threads without duplicates).
	Origin string `json:"origin,omitempty"`
}

// Tickets is the minimal interface every adapter implements.
type Tickets interface {
	Get(ctx context.Context, id string) (*Ticket, error)
	List(ctx context.Context, f Filter) ([]Ticket, error)
	Create(ctx context.Context, n NewTicket, by Actor) (*Ticket, error)
	Update(ctx context.Context, id string, u Update, by Actor) (*Ticket, error)
}

// Threads is the comment thread of tickets.
type Threads interface {
	Comments(ctx context.Context, id string) ([]Comment, error)
	AddComment(ctx context.Context, id, body string, by Actor) (*Comment, error)
}

// OriginCommenter adds a comment that keeps the origin it had in another
// system (live sync, ADR 0003 D-8); optional for adapters.
type OriginCommenter interface {
	AddCommentWithOrigin(ctx context.Context, id, body string, by Actor, origin string) (*Comment, error)
}

// Inbox hands work to agents. Checkout is exclusive: one agent per ticket.
type Inbox interface {
	// Next returns the next open ticket assigned to agentID that no other
	// agent holds – including one agentID itself holds, so partial work is
	// resumed – or (nil, nil) when there is no work.
	Next(ctx context.Context, agentID string) (*Ticket, error)
	// Checkout claims the ticket for agentID; ErrConflict if another agent
	// holds it. Checking out again by the same agent is a no-op.
	Checkout(ctx context.Context, id, agentID string) error
	// Release gives the ticket back; releasing a ticket the agent does not
	// hold is ErrConflict.
	Release(ctx context.Context, id, agentID string) error
}

// Waker wakes an agent so it looks for work now.
type Waker interface {
	Wake(ctx context.Context, agentID string) error
}

// Capabilities tells the core and the UI what an adapter supports.
type Capabilities struct {
	Threads   bool `json:"threads"`
	Inbox     bool `json:"inbox"`
	Children  bool `json:"children"`
	Approvals bool `json:"approvals"`
	Wake      bool `json:"wake"`
	// Authorship: the provider stores the DockWeave actor as ticket author.
	// Without it the author is the provider's API identity; the DockWeave
	// audit trail keeps the real actor either way.
	Authorship bool `json:"authorship"`
}

// Health is the provider's reachability.
type Health struct {
	OK      bool      `json:"ok"`
	Detail  string    `json:"detail,omitempty"`
	Checked time.Time `json:"checked"`
}

// Provider is one ticket backend. Accessors return nil when the capability
// is missing; callers check Capabilities or the nil value.
type Provider interface {
	Name() string
	Health(ctx context.Context) Health
	Capabilities() Capabilities
	Tickets() Tickets
	Threads() Threads
	Inbox() Inbox
	Waker() Waker
}

// Errors shared by all adapters; adapters wrap them with context.
var (
	ErrNotFound    = errors.New("ticket not found")
	ErrConflict    = errors.New("ticket conflict")
	ErrInvalid     = errors.New("invalid ticket request")
	ErrUnsupported = errors.New("not supported by this ticket provider")
	// ErrUnavailable is "ticketing_unavailable": the provider cannot be
	// reached. Callers fail closed; agents are not driven.
	ErrUnavailable = errors.New("ticketing unavailable")
)
