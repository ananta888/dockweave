package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Strategic planning, daily queue, focus sessions and evidence.
// These types are the contract between persistence, API, CLI and TUI.

// ObjectiveKind is a level of the optional goal hierarchy.
type ObjectiveKind string

const (
	ObjectiveVision   ObjectiveKind = "vision"
	ObjectiveYear     ObjectiveKind = "year"
	ObjectiveQuarter  ObjectiveKind = "quarter"
	ObjectiveMonth    ObjectiveKind = "month"
	ObjectiveWeek     ObjectiveKind = "week"
	ObjectiveProject  ObjectiveKind = "project"
	ObjectiveOperativ ObjectiveKind = "operative"
)

// ObjectiveStatus is the lifecycle of a strategic goal.
type ObjectiveStatus string

const (
	ObjectiveActive   ObjectiveStatus = "active"
	ObjectivePaused   ObjectiveStatus = "paused"
	ObjectiveAchieved ObjectiveStatus = "achieved"
	ObjectiveDropped  ObjectiveStatus = "dropped"
	ObjectiveArchived ObjectiveStatus = "archived"
)

// Objective is one node of the optional goal hierarchy.
type Objective struct {
	ID          string          `json:"id"`
	Kind        ObjectiveKind   `json:"kind"`
	ParentID    string          `json:"parent_id,omitempty"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Status      ObjectiveStatus `json:"status"`
	PeriodStart string          `json:"period_start,omitempty"` // YYYY-MM-DD
	PeriodEnd   string          `json:"period_end,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Validate checks the objective before it is stored.
func (o *Objective) Validate() error {
	if strings.TrimSpace(o.Title) == "" {
		return errors.New("objective title must not be empty")
	}
	switch o.Kind {
	case ObjectiveVision, ObjectiveYear, ObjectiveQuarter, ObjectiveMonth,
		ObjectiveWeek, ObjectiveProject, ObjectiveOperativ:
	default:
		return fmt.Errorf("unsupported objective kind %q", o.Kind)
	}
	if o.Status == "" {
		o.Status = ObjectiveActive
	}
	switch o.Status {
	case ObjectiveActive, ObjectivePaused, ObjectiveAchieved, ObjectiveDropped, ObjectiveArchived:
	default:
		return fmt.Errorf("unsupported objective status %q", o.Status)
	}
	return nil
}

// TaskStatus covers human and agent work, including partial results.
type TaskStatus string

const (
	TaskBacklog    TaskStatus = "backlog"
	TaskTodo       TaskStatus = "todo"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskPartial    TaskStatus = "partial"
	TaskFailed     TaskStatus = "failed"
	TaskBlocked    TaskStatus = "blocked"
	TaskVerified   TaskStatus = "verified"
	TaskCancelled  TaskStatus = "cancelled"
)

// AssigneeKind separates human focus work from agent work.
type AssigneeKind string

const (
	AssigneeHuman AssigneeKind = "human"
	AssigneeAgent AssigneeKind = "agent"
)

// Task is a unit of work on the daily queue or an agent order.
type Task struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Description   string       `json:"description,omitempty"`
	Notes         string       `json:"notes,omitempty"`
	ObjectiveIDs  []string     `json:"objective_ids,omitempty"`
	AssigneeKind  AssigneeKind `json:"assignee_kind"`
	AgentID       string       `json:"agent_id,omitempty"`
	WorkspaceID   string       `json:"workspace_id,omitempty"`
	Day           string       `json:"day,omitempty"` // YYYY-MM-DD, empty = backlog
	Status        TaskStatus   `json:"status"`
	Priority      int          `json:"priority"` // 0..100, higher = sooner
	Risk          string       `json:"risk,omitempty"`
	EffortMinutes int          `json:"effort_minutes,omitempty"`
	OrderIndex    int          `json:"order_index"`
	BlockedBy     []string     `json:"blocked_by,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
}

// Validate checks the task before it is stored.
func (t *Task) Validate() error {
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("task title must not be empty")
	}
	if t.AssigneeKind == "" {
		t.AssigneeKind = AssigneeHuman
	}
	switch t.AssigneeKind {
	case AssigneeHuman, AssigneeAgent:
	default:
		return fmt.Errorf("unsupported assignee kind %q", t.AssigneeKind)
	}
	if t.AssigneeKind == AssigneeAgent && t.AgentID == "" {
		return errors.New("agent tasks need an agent_id")
	}
	if t.Status == "" {
		t.Status = TaskTodo
	}
	switch t.Status {
	case TaskBacklog, TaskTodo, TaskInProgress, TaskDone, TaskPartial,
		TaskFailed, TaskBlocked, TaskVerified, TaskCancelled:
	default:
		return fmt.Errorf("unsupported task status %q", t.Status)
	}
	if t.Day != "" {
		if _, err := time.Parse("2006-01-02", t.Day); err != nil {
			return fmt.Errorf("task day must be YYYY-MM-DD: %w", err)
		}
	}
	if t.Priority < 0 || t.Priority > 100 {
		return errors.New("task priority must be between 0 and 100")
	}
	return nil
}

// IsFinished reports whether no further focus time should be spent.
func (t Task) IsFinished() bool {
	switch t.Status {
	case TaskDone, TaskVerified, TaskCancelled:
		return true
	}
	return false
}

// FocusStatus is the lifecycle of a focus session.
type FocusStatus string

const (
	FocusActive    FocusStatus = "active"
	FocusPaused    FocusStatus = "paused"
	FocusCompleted FocusStatus = "completed"
	FocusSkipped   FocusStatus = "skipped"
)

// FocusSession is a single human focus run for one task.
type FocusSession struct {
	ID           string      `json:"id"`
	TaskID       string      `json:"task_id"`
	HumanID      string      `json:"human_id"`
	Status       FocusStatus `json:"status"`
	StartedAt    time.Time   `json:"started_at"`
	EndedAt      *time.Time  `json:"ended_at,omitempty"`
	PausedSecond int         `json:"paused_seconds"`
	Notes        string      `json:"notes,omitempty"`
}

// Elapsed returns the invested time without counting pauses.
func (f FocusSession) Elapsed(now time.Time) time.Duration {
	end := now
	if f.EndedAt != nil {
		end = *f.EndedAt
	}
	d := end.Sub(f.StartedAt) - time.Duration(f.PausedSecond)*time.Second
	if d < 0 {
		return 0
	}
	return d
}

// WorkEventKind classifies measured time or cost.
type WorkEventKind string

const (
	EventHumanFocus WorkEventKind = "human_focus"
	EventAgentRun   WorkEventKind = "agent_run"
	EventContainer  WorkEventKind = "container"
	EventWait       WorkEventKind = "wait"
	EventLLM        WorkEventKind = "llm"
	EventTool       WorkEventKind = "tool"
)

// WorkEvent is one measured interval or cost unit. IdempotencyKey makes
// ingestion safe when a monitor retries.
type WorkEvent struct {
	ID              string            `json:"id"`
	Kind            WorkEventKind     `json:"kind"`
	TaskID          string            `json:"task_id,omitempty"`
	ObjectiveIDs    []string          `json:"objective_ids,omitempty"`
	AgentID         string            `json:"agent_id,omitempty"`
	HumanID         string            `json:"human_id,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         *time.Time        `json:"ended_at,omitempty"`
	DurationSeconds int64             `json:"duration_seconds"`
	Source          string            `json:"source,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// LooksSensitive reports whether a key name suggests a credential or secret.
// Events and evidence must never carry those values.
func LooksSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, needle := range []string{"secret", "password", "passwd", "token", "api_key", "apikey", "private_key", "credential"} {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

// Validate checks a work event.
func (e *WorkEvent) Validate() error {
	switch e.Kind {
	case EventHumanFocus, EventAgentRun, EventContainer, EventWait, EventLLM, EventTool:
	default:
		return fmt.Errorf("unsupported work event kind %q", e.Kind)
	}
	if e.DurationSeconds < 0 {
		return errors.New("duration must not be negative")
	}
	if e.StartedAt.IsZero() {
		return errors.New("started_at is required")
	}
	for k := range e.Meta {
		if LooksSensitive(k) {
			return fmt.Errorf("work event meta must not contain %q", k)
		}
	}
	return nil
}

// EvidenceKind classifies proof of work.
type EvidenceKind string

const (
	EvidenceCommit     EvidenceKind = "commit"
	EvidencePR         EvidenceKind = "pull_request"
	EvidenceFile       EvidenceKind = "file"
	EvidenceTest       EvidenceKind = "test"
	EvidenceBuild      EvidenceKind = "build"
	EvidenceSecurity   EvidenceKind = "security"
	EvidenceReview     EvidenceKind = "review"
	EvidenceArtifact   EvidenceKind = "artifact"
	EvidenceAcceptance EvidenceKind = "acceptance"
)

// EvidenceStatus is the outcome a proof reports.
type EvidenceStatus string

const (
	EvidencePassed  EvidenceStatus = "passed"
	EvidenceFailed  EvidenceStatus = "failed"
	EvidenceUnknown EvidenceStatus = "unknown"
)

// Evidence links a verifiable result to a task. An agent statement alone is
// never evidence: Verified is only set from a deterministic check.
type Evidence struct {
	ID             string            `json:"id"`
	TaskID         string            `json:"task_id"`
	Kind           EvidenceKind      `json:"kind"`
	Status         EvidenceStatus    `json:"status"`
	Ref            string            `json:"ref,omitempty"`
	Verified       bool              `json:"verified"`
	Payload        map[string]string `json:"payload,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// Validate checks evidence.
func (e *Evidence) Validate() error {
	switch e.Kind {
	case EvidenceCommit, EvidencePR, EvidenceFile, EvidenceTest, EvidenceBuild,
		EvidenceSecurity, EvidenceReview, EvidenceArtifact, EvidenceAcceptance:
	default:
		return fmt.Errorf("unsupported evidence kind %q", e.Kind)
	}
	if e.Status == "" {
		e.Status = EvidenceUnknown
	}
	switch e.Status {
	case EvidencePassed, EvidenceFailed, EvidenceUnknown:
	default:
		return fmt.Errorf("unsupported evidence status %q", e.Status)
	}
	if e.TaskID == "" {
		return errors.New("evidence needs a task_id")
	}
	for k := range e.Payload {
		if LooksSensitive(k) {
			return fmt.Errorf("evidence payload must not contain %q", k)
		}
	}
	return nil
}

// BudgetKind is an input budget dimension.
type BudgetKind string

const (
	BudgetHumanHours BudgetKind = "human_hours"
	BudgetAgentHours BudgetKind = "agent_hours"
	BudgetTokens     BudgetKind = "tokens"
	BudgetCostUSD    BudgetKind = "cost_usd"
)

// InputBudget is a configurable target for one scope and period.
type InputBudget struct {
	ID        string     `json:"id"`
	Scope     string     `json:"scope"` // objective|workspace|agent|global
	ScopeID   string     `json:"scope_id,omitempty"`
	Kind      BudgetKind `json:"kind"`
	Period    string     `json:"period"` // weekly|monthly|total
	Target    float64    `json:"target"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Validate checks the input budget.
func (b *InputBudget) Validate() error {
	switch b.Scope {
	case "objective", "workspace", "agent", "global":
	default:
		return fmt.Errorf("unsupported budget scope %q", b.Scope)
	}
	switch b.Kind {
	case BudgetHumanHours, BudgetAgentHours, BudgetTokens, BudgetCostUSD:
	default:
		return fmt.Errorf("unsupported budget kind %q", b.Kind)
	}
	switch b.Period {
	case "weekly", "monthly", "total":
	default:
		return fmt.Errorf("unsupported budget period %q", b.Period)
	}
	if b.Target < 0 {
		return errors.New("budget target must not be negative")
	}
	return nil
}

// ReviewKind is weekly or monthly reflection.
type ReviewKind string

const (
	ReviewWeekly  ReviewKind = "weekly"
	ReviewMonthly ReviewKind = "monthly"
)

// Review is a stored reflection snapshot.
type Review struct {
	ID          string         `json:"id"`
	Kind        ReviewKind     `json:"kind"`
	PeriodStart string         `json:"period_start"`
	PeriodEnd   string         `json:"period_end"`
	Body        map[string]any `json:"body"`
	CreatedAt   time.Time      `json:"created_at"`
}

// TodayPlan is the deterministic daily queue plus its focus state.
type TodayPlan struct {
	Day         string        `json:"day"`
	Tasks       []*Task       `json:"tasks"`
	Focus       *FocusSession `json:"focus,omitempty"`
	ActiveAgent int           `json:"active_agents"`
}
