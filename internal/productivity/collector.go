// Package productivity contains the deterministic collector that turns
// presence and broker counters into work_events. No LLM calls, no heuristics
// beyond the documented presence rule.
package productivity

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ananta888/dockweave/internal/brokerclient"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// Collector periodically records agent runtime and LLM usage.
type Collector struct {
	Store    *persistence.Store
	Broker   *brokerclient.Client
	Interval time.Duration
}

type brokerSnapshot struct {
	Day      string  `json:"day"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
	Requests int     `json:"requests"`
}

// Run blocks until the context is cancelled.
func (c *Collector) Run(ctx context.Context) {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(ctx)
		}
	}
}

func (c *Collector) tick(ctx context.Context) {
	c.collectAgentRuns(ctx)
	if c.Broker != nil {
		c.collectBrokerUsage(ctx)
	}
}

// collectAgentRuns records one minute of runtime per running agent. The rule is
// presence-based: a running container counts as active agent runtime. The
// measurement is idempotent per agent and minute.
func (c *Collector) collectAgentRuns(ctx context.Context) {
	agents, err := c.Store.ListAgents(ctx)
	if err != nil {
		return
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	end := minute.Add(time.Minute)
	for _, a := range agents {
		if a.State != model.AgentRunning {
			continue
		}
		ev := &model.WorkEvent{
			Kind: model.EventAgentRun, AgentID: a.ID,
			StartedAt: minute, EndedAt: &end, DurationSeconds: 60,
			Source:         "collector:presence",
			Meta:           map[string]string{"rule": "running container"},
			IdempotencyKey: fmt.Sprintf("agent:%s:%d", a.ID, minute.Unix()),
		}
		if _, err := c.Store.AddWorkEvent(ctx, ev); err != nil {
			log.Printf("productivity collector: agent event: %v", err)
		}
	}
}

// collectBrokerUsage turns broker counter deltas into LLM events with token and
// cost metadata. The previous snapshot is stored in the meta table, so a
// restart cannot double count.
func (c *Collector) collectBrokerUsage(ctx context.Context) {
	grants, err := c.Broker.ListGrants(ctx, "")
	if err != nil {
		return
	}
	agents, _ := c.Store.ListAgents(ctx)
	byName := map[string]*model.Agent{}
	for _, a := range agents {
		byName[a.Name] = a
	}
	for _, g := range grants {
		usage, err := c.Broker.Usage(ctx, g.ID)
		if err != nil || usage == nil {
			continue
		}
		key := "productivity:broker:" + g.ID
		cur := brokerSnapshot{
			Day: usage.Day, Tokens: usage.TokensDay,
			Cost: usage.CostDayUSD, Requests: usage.RequestsDay,
		}
		raw, _ := c.Store.GetMeta(ctx, key)
		var prev brokerSnapshot
		_ = json.Unmarshal([]byte(raw), &prev)

		dTokens, dCost, dReq := cur.Tokens, cur.Cost, cur.Requests
		if prev.Day == cur.Day {
			dTokens -= prev.Tokens
			dCost -= prev.Cost
			dReq -= prev.Requests
		}
		if dTokens > 0 || dCost > 0 || dReq > 0 {
			agentID := g.Agent
			if a, ok := byName[g.Agent]; ok {
				agentID = a.ID
			}
			now := time.Now().UTC()
			ev := &model.WorkEvent{
				Kind: model.EventLLM, AgentID: agentID, StartedAt: now,
				DurationSeconds: 0, Source: "collector:broker",
				Meta: map[string]string{
					"tokens":   fmt.Sprintf("%d", dTokens),
					"cost_usd": fmt.Sprintf("%.6f", dCost),
					"requests": fmt.Sprintf("%d", dReq),
					"grant":    g.ID,
				},
				IdempotencyKey: fmt.Sprintf("broker:%s:%s:%d:%d", g.ID, cur.Day, cur.Tokens, int64(cur.Cost*1e6)),
			}
			if _, err := c.Store.AddWorkEvent(ctx, ev); err != nil {
				log.Printf("productivity collector: llm event: %v", err)
			}
		}
		body, _ := json.Marshal(cur)
		_ = c.Store.SetMeta(ctx, key, string(body))
	}
}
