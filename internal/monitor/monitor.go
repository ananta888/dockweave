// Package monitor observes agents through modular providers. Providers never
// fabricate data: when a source is unavailable, the corresponding field is
// explicitly marked unavailable.
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// Provider is a modular observation source.
type Provider interface {
	// Name identifies the provider (e.g. "docker", "procfs").
	Name() string
	// Available reports whether the provider can deliver data in this
	// environment, with a reason if not.
	Available(ctx context.Context) error
	// Observe fills in provider-specific data of the activity record.
	Observe(ctx context.Context, act *model.AgentActivity) error
}

// Monitor aggregates providers into activity snapshots and conflicts.
type Monitor struct {
	providers []Provider
	detect    *ConflictDetector
}

// New builds a Monitor from providers and a conflict detector.
func New(providers ...Provider) *Monitor {
	return &Monitor{providers: providers, detect: NewConflictDetector()}
}

// DetectorRoots sets the workspace-root resolver used for conflict detection.
func (m *Monitor) DetectorRoots(fn func() map[string]string) {
	m.detect.Roots = fn
}

// Providers returns the registered providers and their availability.
func (m *Monitor) Providers(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, p := range m.providers {
		if err := p.Available(ctx); err != nil {
			out[p.Name()] = "unavailable: " + err.Error()
		} else {
			out[p.Name()] = "ok"
		}
	}
	return out
}

// Collect builds an activity snapshot for every agent. Each provider
// contributes; failures degrade the record instead of failing the snapshot.
func (m *Monitor) Collect(ctx context.Context, agents []*model.Agent) []*model.AgentActivity {
	out := make([]*model.AgentActivity, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Add(1)
		go func(i int, a *model.Agent) {
			defer wg.Done()
			act := &model.AgentActivity{
				AgentID:   a.ID,
				State:     string(a.State),
				Activity:  model.ActivityUnknown,
				Available: true,
				Timestamp: time.Now(),
			}
			for _, p := range m.providers {
				if err := p.Available(ctx); err != nil {
					if act.Unavailable == "" {
						act.Unavailable = p.Name() + ": " + err.Error()
					} else {
						act.Unavailable += "; " + p.Name() + ": " + err.Error()
					}
					continue
				}
				if err := p.Observe(ctx, act); err != nil {
					if act.Unavailable == "" {
						act.Unavailable = p.Name() + ": " + err.Error()
					} else {
						act.Unavailable += "; " + p.Name() + ": " + err.Error()
					}
				}
			}
			out[i] = act
		}(i, a)
	}
	wg.Wait()
	return out
}

// Conflicts returns current multi-agent file conflicts (evidence: fds held
// open by processes of multiple agents, at least one for writing).
func (m *Monitor) Conflicts(ctx context.Context) ([]model.FileConflict, error) {
	return m.detect.Conflicts(ctx)
}

// WorkspaceOfActivity maps an observed open file to the workspace mount it
// belongs to (longest registered mount target wins).
func WorkspaceOfActivity(path string, mountTargets []string) string {
	best := ""
	for _, t := range mountTargets {
		if len(t) > len(best) && hasPathPrefix(path, t) {
			best = t
		}
	}
	return best
}

func hasPathPrefix(p, prefix string) bool {
	if prefix == "" || len(p) < len(prefix) {
		return false
	}
	if p[:len(prefix)] != prefix {
		return false
	}
	return len(p) == len(prefix) || p[len(prefix)] == '/'
}

var _ = fmt.Sprintf // keep fmt for future formatting helpers
