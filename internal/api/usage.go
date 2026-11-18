package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

type usageWindow struct {
	UsedPercent   float64    `json:"used_percent"`
	WindowMinutes int        `json:"window_minutes,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
}

type usageCredits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

type tokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

type agentUsage struct {
	AgentID     string        `json:"agent_id"`
	Agent       string        `json:"agent"`
	DisplayName string        `json:"display_name,omitempty"`
	Harness     string        `json:"harness"`
	Runtime     string        `json:"runtime,omitempty"`
	Source      string        `json:"source"`
	Available   bool          `json:"available"`
	Note        string        `json:"note,omitempty"`
	FiveHour    *usageWindow  `json:"five_hour,omitempty"`
	SevenDay    *usageWindow  `json:"seven_day,omitempty"`
	Credits     *usageCredits `json:"credits,omitempty"`
	CostUSD     float64       `json:"cost_usd,omitempty"`
	Tokens      *tokenUsage   `json:"tokens,omitempty"`
	Sessions    int           `json:"sessions,omitempty"`
	FetchedAt   *time.Time    `json:"fetched_at,omitempty"`
}

// usage returns subscription/plan usage for the running harnesses where the
// harness exposes it locally. Secret values are never involved.
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Agents.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]agentUsage, 0, len(agents))
	for _, a := range agents {
		u := agentUsage{AgentID: a.ID, Agent: a.Name, DisplayName: a.DisplayName,
			Harness: a.Type, Runtime: string(a.Runtime)}
		paths, err := s.Agents.AgentPaths(r.Context(), a.ID)
		if err != nil {
			u.Note = "agent paths unavailable"
			out = append(out, u)
			continue
		}
		switch a.Type {
		case "codex":
			u = codexUsage(a, paths.MemoryDir, u)
		case "claude":
			u = claudeUsage(paths.MemoryDir, u)
		case "opencode":
			u = s.opencodeUsage(r, a, u)
		default:
			u.Note = "usage not exposed by this harness"
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

type codexRateLimits struct {
	Primary *struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	} `json:"primary"`
	Secondary *struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	} `json:"secondary"`
	Credits *struct {
		HasCredits bool   `json:"has_credits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
}

func codexUsage(a *model.Agent, memDir string, u agentUsage) agentUsage {
	u.Source = "codex rollout"
	root := filepath.Join(memDir, ".codex", "sessions")
	var newest string
	var newestMod time.Time
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(newestMod) {
			newest, newestMod = p, fi.ModTime()
		}
		return nil
	})
	if newest == "" {
		u.Note = "no Codex rollout yet"
		return u
	}
	f, err := os.Open(newest)
	if err != nil {
		u.Note = "Codex rollout unreadable"
		return u
	}
	defer f.Close()
	var last *codexRateLimits
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				Type       string           `json:"type"`
				RateLimits *codexRateLimits `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type == "event_msg" && ev.Payload.Type == "token_count" && ev.Payload.RateLimits != nil {
			last = ev.Payload.RateLimits
		}
	}
	if last == nil {
		u.Note = "Codex rollout has no rate-limit event"
		return u
	}
	mk := func(w *struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	}) *usageWindow {
		if w == nil {
			return nil
		}
		out := &usageWindow{UsedPercent: w.UsedPercent, WindowMinutes: w.WindowMinutes}
		if w.ResetsAt > 0 {
			t := time.Unix(w.ResetsAt, 0)
			out.ResetsAt = &t
		}
		return out
	}
	u.FiveHour = mk(last.Primary)
	u.SevenDay = mk(last.Secondary)
	if last.Credits != nil {
		u.Credits = &usageCredits{HasCredits: last.Credits.HasCredits,
			Unlimited: last.Credits.Unlimited, Balance: last.Credits.Balance}
	}
	u.Available = true
	u.FetchedAt = &newestMod
	u.Note = "ChatGPT plan windows from the latest Codex rollout"
	return u
}

type claudeCache struct {
	FetchedAtMs int64 `json:"fetchedAtMs"`
	Utilization struct {
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
		Spend *struct {
			Used struct {
				AmountMinor int    `json:"amount_minor"`
				Currency    string `json:"currency"`
				Exponent    int    `json:"exponent"`
			} `json:"used"`
			Enabled bool `json:"enabled"`
		} `json:"spend"`
	} `json:"utilization"`
}

func claudeUsage(memDir string, u agentUsage) agentUsage {
	u.Source = "claude cache"
	b, err := os.ReadFile(filepath.Join(memDir, ".claude.json"))
	if err != nil {
		u.Note = "Claude config not found"
		return u
	}
	var root struct {
		Cache *claudeCache `json:"cachedUsageUtilization"`
	}
	if json.Unmarshal(b, &root) != nil || root.Cache == nil {
		u.Note = "Claude usage cache not populated (open /usage once)"
		return u
	}
	parseWindow := func(pct float64, reset string) *usageWindow {
		w := &usageWindow{UsedPercent: pct}
		if t, err := time.Parse(time.RFC3339Nano, reset); err == nil {
			w.ResetsAt = &t
		}
		return w
	}
	if root.Cache.Utilization.FiveHour != nil {
		u.FiveHour = parseWindow(root.Cache.Utilization.FiveHour.Utilization, root.Cache.Utilization.FiveHour.ResetsAt)
	}
	if root.Cache.Utilization.SevenDay != nil {
		u.SevenDay = parseWindow(root.Cache.Utilization.SevenDay.Utilization, root.Cache.Utilization.SevenDay.ResetsAt)
	}
	if root.Cache.FetchedAtMs > 0 {
		t := time.UnixMilli(root.Cache.FetchedAtMs)
		u.FetchedAt = &t
	}
	if root.Cache.Utilization.Spend != nil && root.Cache.Utilization.Spend.Enabled {
		exp := root.Cache.Utilization.Spend.Used.Exponent
		div := 1.0
		for i := 0; i < exp; i++ {
			div *= 10
		}
		u.CostUSD = float64(root.Cache.Utilization.Spend.Used.AmountMinor) / div
	}
	u.Available = u.FiveHour != nil || u.SevenDay != nil
	u.Note = "Claude plan usage from the local cache (refreshed by /usage)"
	return u
}

const opencodeUsageScript = `
import base64, json, sys, urllib.request
url, password = sys.argv[1:3]
auth = "Basic " + base64.b64encode(("opencode:" + password).encode()).decode()
req = urllib.request.Request(url + "/api/session", headers={"Authorization": auth})
with urllib.request.urlopen(req, timeout=15) as resp:
    sessions = json.load(resp).get("data", [])
cost = 0.0
tokens = {"input": 0, "output": 0, "reasoning": 0, "cache_read": 0, "cache_write": 0}
for s in sessions:
    cost += float(s.get("cost") or 0)
    t = s.get("tokens") or {}
    for k in ("input", "output", "reasoning"):
        tokens[k] += int(t.get(k) or 0)
    c = t.get("cache") or {}
    tokens["cache_read"] += int(c.get("read") or 0)
    tokens["cache_write"] += int(c.get("write") or 0)
tokens["total"] = tokens["input"] + tokens["output"] + tokens["reasoning"]
print(json.dumps({"cost": cost, "sessions": len(sessions), "tokens": tokens}))
`

func (s *Server) opencodeUsage(r *http.Request, a *model.Agent, u agentUsage) agentUsage {
	u.Source = "opencode sessions"
	cid, err := s.Agents.Docker.FindByAgent(r.Context(), a.ID)
	if err != nil || cid == "" {
		u.Note = "opencode server is not running"
		return u
	}
	raw, err := s.Agents.Docker.ExecOutput(r.Context(), cid, []string{"cat", "/memory/.local/state/opencode/service.json"})
	if err != nil {
		u.Note = "opencode service metadata unavailable"
		return u
	}
	var svc struct {
		URL      string `json:"url"`
		Password string `json:"password"`
	}
	if json.Unmarshal([]byte(raw), &svc) != nil || svc.URL == "" {
		u.Note = "opencode service metadata invalid"
		return u
	}
	out, err := s.Agents.Docker.ExecOutput(r.Context(), cid, []string{
		"python3", "-c", opencodeUsageScript, svc.URL, svc.Password,
	})
	if err != nil {
		u.Note = "opencode session usage query failed"
		return u
	}
	var res struct {
		Cost     float64    `json:"cost"`
		Sessions int        `json:"sessions"`
		Tokens   tokenUsage `json:"tokens"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &res) != nil {
		u.Note = "opencode session usage invalid"
		return u
	}
	u.CostUSD = res.Cost
	u.Sessions = res.Sessions
	u.Tokens = &res.Tokens
	u.Available = true
	u.Note = "local OpenCode session usage; subscription credits are not exposed by the local server"
	if res.Cost > 0 {
		u.Note = "OpenCode session cost and tokens (local)"
	}
	return u
}
