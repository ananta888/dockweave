// Package aimonitor implements the optional DockWeave AI monitor agent.
// It works EXCLUSIVELY on the restricted monitoring API (read activity,
// conflicts; submit reports). It holds no privileges and cannot change
// grants, mounts or agent state.
//
// The analysis is deterministic and rule-based; it never fabricates data —
// every finding cites the evidence it was derived from.
package aimonitor

import (
	"fmt"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Findings classify report items.
type Finding struct {
	Kind    string `json:"kind"`    // conflict | inactivity | resource | distribution
	Severity string `json:"severity"` // info | warning | critical
	Message string `json:"message"`
}

// Report is the analysis result of one observation cycle.
type Report struct {
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Findings  []Finding `json:"findings"`
	Generated time.Time `json:"generated_at"`
}

// Config parameterizes the analyzer.
type Config struct {
	// Inactivity marks running agents with IDLE activity older than this.
	Inactivity time.Duration
	// CPUThresholdPercent flags agents above this CPU usage.
	CPUThresholdPercent float64
	// MemThresholdMB flags agents above this resident memory.
	MemThresholdMB int64
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{Inactivity: 15 * time.Minute, CPUThresholdPercent: 90, MemThresholdMB: 4096}
}

// State tracks activity history across cycles.
type State struct {
	// lastActive records when an agent last showed READING/WRITING activity.
	lastActive map[string]time.Time
}

// NewState creates empty tracking state.
func NewState() *State { return &State{lastActive: map[string]time.Time{}} }

// Analyze derives findings from one activity snapshot. now allows
// deterministic tests.
func Analyze(s *State, acts []*model.AgentActivity, conflicts []model.FileConflict, cfg Config, now time.Time) *Report {
	if s == nil {
		s = NewState()
	}
	rep := &Report{Title: "DockWeave activity report", Generated: now}
	var running, active int
	for _, a := range acts {
		if !a.Available {
			rep.Findings = append(rep.Findings, Finding{Kind: "resource", Severity: "warning",
				Message: fmt.Sprintf("agent %s: monitoring data unavailable (%s)", short(a.AgentID), a.Unavailable)})
			continue
		}
		if a.State == "running" {
			running++
			if a.Activity == model.ActivityReading || a.Activity == model.ActivityWriting {
				active++
				s.lastActive[a.AgentID] = now
			} else if cfg.Inactivity > 0 {
				if last, ok := s.lastActive[a.AgentID]; ok && now.Sub(last) > cfg.Inactivity {
					rep.Findings = append(rep.Findings, Finding{Kind: "inactivity", Severity: "info",
						Message: fmt.Sprintf("agent %s running but idle for %s (last activity %s)",
							short(a.AgentID), now.Sub(last).Round(time.Minute), last.Format(time.RFC3339))})
				}
			}
		}
		if cfg.CPUThresholdPercent > 0 && a.CPUPercent > cfg.CPUThresholdPercent {
			rep.Findings = append(rep.Findings, Finding{Kind: "resource", Severity: "warning",
				Message: fmt.Sprintf("agent %s CPU %.1f%% above threshold %.0f%%",
					short(a.AgentID), a.CPUPercent, cfg.CPUThresholdPercent)})
		}
		if cfg.MemThresholdMB > 0 && a.MemoryBytes > uint64(cfg.MemThresholdMB)*1024*1024 {
			rep.Findings = append(rep.Findings, Finding{Kind: "resource", Severity: "warning",
				Message: fmt.Sprintf("agent %s memory %d MiB above threshold %d MiB",
					short(a.AgentID), a.MemoryBytes/1024/1024, cfg.MemThresholdMB)})
		}
	}
	for _, c := range conflicts {
		writers := strings.Join(c.Writers, ", ")
		rep.Findings = append(rep.Findings, Finding{Kind: "conflict", Severity: "critical",
			Message: fmt.Sprintf("file %s held by agents %v with concurrent writer(s) %s — coordinate access or use exclusive grants",
				c.Path, c.AgentIDs, writers)})
	}
	// Work distribution hint.
	if running > 1 && active == 0 {
		rep.Findings = append(rep.Findings, Finding{Kind: "distribution", Severity: "info",
			Message: fmt.Sprintf("%d agents running but none active — consider reallocating workspaces", running)})
	}
	rep.Summary = fmt.Sprintf("%d agents observed, %d running, %d active, %d conflicts, %d findings",
		len(acts), running, active, len(conflicts), len(rep.Findings))
	return rep
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
