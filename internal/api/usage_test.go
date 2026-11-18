package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
)

func TestCodexUsageParsesRateLimits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".codex", "sessions", "2026", "11", "18", "rollout-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":12.5,"window_minutes":300,"resets_at":1795016952},"secondary":{"used_percent":3,"window_minutes":10080,"resets_at":1795128343},"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}}}`
	if err := os.WriteFile(p, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := codexUsage(&model.Agent{}, dir, agentUsage{})
	if !u.Available || u.FiveHour == nil || u.SevenDay == nil || u.Credits == nil {
		t.Fatalf("usage = %+v", u)
	}
	if u.FiveHour.UsedPercent != 12.5 || u.SevenDay.UsedPercent != 3 || u.FiveHour.WindowMinutes != 300 {
		t.Fatalf("windows = %+v / %+v", u.FiveHour, u.SevenDay)
	}
	if u.Credits.Balance != "0" {
		t.Fatalf("credits = %+v", u.Credits)
	}
}

func TestClaudeUsageParsesCache(t *testing.T) {
	dir := t.TempDir()
	b := `{"cachedUsageUtilization":{"fetchedAtMs":1794994949375,"utilization":{"five_hour":{"utilization":17,"resets_at":"2026-11-18T12:49:59.695888+00:00"},"seven_day":{"utilization":37,"resets_at":"2026-11-21T10:59:59.695907+00:00"},"spend":{"enabled":false}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	u := claudeUsage(dir, agentUsage{})
	if !u.Available || u.FiveHour == nil || u.SevenDay == nil {
		t.Fatalf("usage = %+v", u)
	}
	if u.FiveHour.UsedPercent != 17 || u.SevenDay.UsedPercent != 37 {
		t.Fatalf("windows = %+v / %+v", u.FiveHour, u.SevenDay)
	}
	if u.FetchedAt == nil {
		t.Fatal("fetched_at missing")
	}
}
