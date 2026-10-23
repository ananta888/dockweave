package aimonitor

import (
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func mk(agentID string, running bool, act model.ActivityLevel, cpu float64, mem uint64) *model.AgentActivity {
	st := "exited"
	if running {
		st = "running"
	}
	return &model.AgentActivity{AgentID: agentID, State: st, Activity: act, CPUPercent: cpu, MemoryBytes: mem, Available: true}
}

func TestConflictFinding(t *testing.T) {
	s := NewState()
	conf := []model.FileConflict{{Path: "dev:ino", AgentIDs: []string{"a", "b"}, Writers: []string{"a"}}}
	rep := Analyze(s, []*model.AgentActivity{mk("a", true, model.ActivityWriting, 0, 0), mk("b", true, model.ActivityReading, 0, 0)}, conf, DefaultConfig(), time.Now())
	found := false
	for _, f := range rep.Findings {
		if f.Kind == "conflict" && f.Severity == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict not reported: %+v", rep.Findings)
	}
}

func TestInactivityAfterWindow(t *testing.T) {
	s := NewState()
	now := time.Now()
	// First cycle: agent active.
	Analyze(s, []*model.AgentActivity{mk("a", true, model.ActivityWriting, 0, 0)}, nil, DefaultConfig(), now)
	// Second cycle 20 minutes later: idle.
	rep := Analyze(s, []*model.AgentActivity{mk("a", true, model.ActivityIdle, 0, 0)}, nil, DefaultConfig(), now.Add(20*time.Minute))
	found := false
	for _, f := range rep.Findings {
		if f.Kind == "inactivity" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inactivity not reported: %+v", rep.Findings)
	}
	// Only 5 minutes later: no finding yet.
	rep = Analyze(NewState(), []*model.AgentActivity{mk("a", true, model.ActivityIdle, 0, 0)}, nil, DefaultConfig(), now.Add(5*time.Minute))
	for _, f := range rep.Findings {
		if f.Kind == "inactivity" {
			t.Fatal("inactivity fired before window elapsed")
		}
	}
}

func TestResourceThresholds(t *testing.T) {
	rep := Analyze(NewState(),
		[]*model.AgentActivity{mk("a", true, model.ActivityReading, 95.5, 8*1024*1024*1024)},
		nil, DefaultConfig(), time.Now())
	cpu, mem := false, false
	for _, f := range rep.Findings {
		if f.Kind != "resource" || f.Severity != "warning" {
			continue
		}
		if contains(f.Message, "CPU") {
			cpu = true
		}
		if contains(f.Message, "memory") {
			mem = true
		}
	}
	if !cpu || !mem {
		t.Fatalf("resource findings incomplete: cpu=%v mem=%v (%+v)", cpu, mem, rep.Findings)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestUnavailableDegradesNotFails(t *testing.T) {
	a := mk("a", true, model.ActivityUnknown, 0, 0)
	a.Available = false
	a.Unavailable = "docker: engine down"
	rep := Analyze(NewState(), []*model.AgentActivity{a}, nil, DefaultConfig(), time.Now())
	warned := false
	for _, f := range rep.Findings {
		if f.Kind == "resource" && f.Severity == "warning" && a.Unavailable != "" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("unavailability must be reported, got %+v", rep.Findings)
	}
}

func TestDistributionHint(t *testing.T) {
	rep := Analyze(NewState(),
		[]*model.AgentActivity{mk("a", true, model.ActivityIdle, 0, 0), mk("b", true, model.ActivityIdle, 0, 0)},
		nil, DefaultConfig(), time.Now())
	found := false
	for _, f := range rep.Findings {
		if f.Kind == "distribution" {
			found = true
		}
	}
	if !found {
		t.Fatalf("distribution hint missing: %+v", rep.Findings)
	}
}
