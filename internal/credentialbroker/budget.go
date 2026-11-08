package credentialbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// ErrLimit is returned when a rate, concurrency, request, token or cost
// limit would be exceeded. It is intentionally coarse: callers must not
// learn which budget is exhausted beyond the operator-facing status API.
var ErrLimit = errors.New("credential budget or rate limit exceeded")

type grantCounter struct {
	Day       string  `json:"day"`
	Month     string  `json:"month"`
	ReqDay    int     `json:"req_day"`
	ReqMonth  int     `json:"req_month"`
	TokDay    int64   `json:"tok_day"`
	TokMonth  int64   `json:"tok_month"`
	CostDay   float64 `json:"cost_day"`
	CostMonth float64 `json:"cost_month"`
}

type budgetFile struct {
	Version  int                      `json:"version"`
	Counters map[string]*grantCounter `json:"counters"`
}

// Budget enforces per-grant request/token/cost limits and concurrency.
// Counters are persisted (UTC day/month buckets) so a restart does not reset
// spend. This is a broker-side estimate, not a provider billing guarantee.
type Budget struct {
	mu       sync.Mutex
	path     string
	counters map[string]*grantCounter
	sems     map[string]chan struct{}
	windows  map[string][]time.Time
	now      func() time.Time
}

// OpenBudget loads persisted counters from path.
func OpenBudget(path string) (*Budget, error) {
	b := &Budget{
		path: path, counters: map[string]*grantCounter{},
		sems: map[string]chan struct{}{}, windows: map[string][]time.Time{},
		now: time.Now,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return b, nil
		}
		return nil, fmt.Errorf("read budget: %w", err)
	}
	var f budgetFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse budget: %w", err)
	}
	if f.Counters != nil {
		b.counters = f.Counters
	}
	return b, nil
}

// Reservation holds a concurrency slot and reserved budget. Callers must
// call Commit or Release exactly once.
type Reservation struct {
	b         *Budget
	grantID   string
	estTokens int64
	estCost   float64
	sem       chan struct{}
	done      bool
}

func dayKey(t time.Time) string   { return t.UTC().Format("2006-01-02") }
func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }

// Acquire checks and reserves estimated usage for one request.
func (b *Budget) Acquire(g Grant, estPrompt, estCompletion int64) (*Reservation, error) {
	if estPrompt < 0 {
		estPrompt = 0
	}
	if estCompletion < 0 {
		estCompletion = 0
	}
	estTokens := estPrompt + estCompletion
	estCost := float64(estPrompt)/1000*g.Limits.PromptUSDPer1K +
		float64(estCompletion)/1000*g.Limits.CompletionUSDPer1K

	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	day, month := dayKey(now), monthKey(now)

	// Sliding one-minute request window.
	win := b.windows[g.ID]
	cutoff := now.Add(-time.Minute)
	i := 0
	for i < len(win) && win[i].Before(cutoff) {
		i++
	}
	win = append(win[i:], now)
	if g.Limits.MaxRequestsPerMinute > 0 && len(win) > g.Limits.MaxRequestsPerMinute {
		b.windows[g.ID] = win[:len(win)-1]
		return nil, ErrLimit
	}
	b.windows[g.ID] = win

	// Concurrency semaphore (non-blocking: over-limit is a hard failure).
	maxConc := g.Limits.MaxConcurrent
	if maxConc <= 0 {
		maxConc = DefaultLimits().MaxConcurrent
	}
	sem, ok := b.sems[g.ID]
	if !ok || cap(sem) != maxConc {
		sem = make(chan struct{}, maxConc)
		b.sems[g.ID] = sem
	}
	select {
	case sem <- struct{}{}:
	default:
		return nil, ErrLimit
	}

	c := b.counterLocked(g.ID, day, month)
	if g.Limits.MaxRequestsPerDay > 0 && c.ReqDay+1 > g.Limits.MaxRequestsPerDay {
		<-sem
		return nil, ErrLimit
	}
	if g.Limits.MaxRequestsPerMonth > 0 && c.ReqMonth+1 > g.Limits.MaxRequestsPerMonth {
		<-sem
		return nil, ErrLimit
	}
	if g.Limits.MaxTokensPerDay > 0 && c.TokDay+estTokens > g.Limits.MaxTokensPerDay {
		<-sem
		return nil, ErrLimit
	}
	if g.Limits.MaxTokensPerMonth > 0 && c.TokMonth+estTokens > g.Limits.MaxTokensPerMonth {
		<-sem
		return nil, ErrLimit
	}
	if g.Limits.MaxCostUSDPerDay > 0 && c.CostDay+estCost > g.Limits.MaxCostUSDPerDay {
		<-sem
		return nil, ErrLimit
	}
	if g.Limits.MaxCostUSDPerMonth > 0 && c.CostMonth+estCost > g.Limits.MaxCostUSDPerMonth {
		<-sem
		return nil, ErrLimit
	}
	c.ReqDay++
	c.ReqMonth++
	c.TokDay += estTokens
	c.TokMonth += estTokens
	c.CostDay += estCost
	c.CostMonth += estCost
	_ = b.saveLocked()

	return &Reservation{b: b, grantID: g.ID, estTokens: estTokens,
		estCost: estCost, sem: sem}, nil
}

// Commit replaces reserved estimates with observed usage and releases the
// concurrency slot.
func (r *Reservation) Commit(promptTokens, completionTokens int64, g Grant) {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	actualTokens := promptTokens + completionTokens
	actualCost := float64(promptTokens)/1000*g.Limits.PromptUSDPer1K +
		float64(completionTokens)/1000*g.Limits.CompletionUSDPer1K
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	c := r.b.counterLocked(r.grantID, dayKey(r.b.now()), monthKey(r.b.now()))
	c.TokDay += actualTokens - r.estTokens
	c.TokMonth += actualTokens - r.estTokens
	c.CostDay += actualCost - r.estCost
	c.CostMonth += actualCost - r.estCost
	if c.TokDay < 0 {
		c.TokDay = 0
	}
	if c.TokMonth < 0 {
		c.TokMonth = 0
	}
	if c.CostDay < 0 {
		c.CostDay = 0
	}
	if c.CostMonth < 0 {
		c.CostMonth = 0
	}
	_ = r.b.saveLocked()
	<-r.sem
}

// Release rolls back reserved estimates and releases the concurrency slot
// (used when the upstream request never completed).
func (r *Reservation) Release() {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	c := r.b.counterLocked(r.grantID, dayKey(r.b.now()), monthKey(r.b.now()))
	c.ReqDay--
	c.ReqMonth--
	c.TokDay -= r.estTokens
	c.TokMonth -= r.estTokens
	c.CostDay -= r.estCost
	c.CostMonth -= r.estCost
	if c.ReqDay < 0 {
		c.ReqDay = 0
	}
	if c.ReqMonth < 0 {
		c.ReqMonth = 0
	}
	if c.TokDay < 0 {
		c.TokDay = 0
	}
	if c.TokMonth < 0 {
		c.TokMonth = 0
	}
	if c.CostDay < 0 {
		c.CostDay = 0
	}
	if c.CostMonth < 0 {
		c.CostMonth = 0
	}
	_ = r.b.saveLocked()
	<-r.sem
}

func (b *Budget) counterLocked(id, day, month string) *grantCounter {
	c, ok := b.counters[id]
	if !ok {
		c = &grantCounter{}
		b.counters[id] = c
	}
	if c.Day != day {
		c.Day = day
		c.ReqDay, c.TokDay, c.CostDay = 0, 0, 0
	}
	if c.Month != month {
		c.Month = month
		c.ReqMonth, c.TokMonth, c.CostMonth = 0, 0, 0
	}
	return c
}

// Usage is the operator-facing budget snapshot for one grant.
type Usage struct {
	GrantID       string  `json:"grant_id"`
	Day           string  `json:"day"`
	Month         string  `json:"month"`
	RequestsDay   int     `json:"requests_day"`
	RequestsMonth int     `json:"requests_month"`
	TokensDay     int64   `json:"tokens_day"`
	TokensMonth   int64   `json:"tokens_month"`
	CostDayUSD    float64 `json:"cost_day_usd"`
	CostMonthUSD  float64 `json:"cost_month_usd"`
}

// Snapshot returns budget counters for a grant.
func (b *Budget) Snapshot(grantID string) Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	c := b.counterLocked(grantID, dayKey(now), monthKey(now))
	return Usage{
		GrantID: grantID, Day: c.Day, Month: c.Month,
		RequestsDay: c.ReqDay, RequestsMonth: c.ReqMonth,
		TokensDay: c.TokDay, TokensMonth: c.TokMonth,
		CostDayUSD: c.CostDay, CostMonthUSD: c.CostMonth,
	}
}

func (b *Budget) saveLocked() error {
	data, err := json.MarshalIndent(budgetFile{Version: 1, Counters: b.counters}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(b.path, data, 0o600)
}
