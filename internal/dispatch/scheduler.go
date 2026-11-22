package dispatch

import (
	"context"
	"sync"
	"time"
)

// Scheduler drives heartbeats for the native provider (ADR 0003 D-9):
// every Interval each listed agent gets one RunOnce; Kick runs an agent now
// (wake). At most Concurrency heartbeats run at once; the dispatcher itself
// allows only one per agent.
type Scheduler struct {
	D           *Dispatcher
	Agents      func(ctx context.Context) ([]string, error)
	Interval    time.Duration // default 30 s
	Concurrency int           // default 4
	Logf        func(format string, args ...any)

	once sync.Once
	kick chan string
	sem  chan struct{}
}

func (s *Scheduler) init() {
	s.once.Do(func() {
		if s.Interval <= 0 {
			s.Interval = 30 * time.Second
		}
		if s.Concurrency <= 0 {
			s.Concurrency = 4
		}
		s.kick = make(chan string, 64)
		s.sem = make(chan struct{}, s.Concurrency)
	})
}

// Kick asks for an immediate heartbeat of agent (never blocks; a full queue
// drops the kick – the next tick picks the agent up anyway).
func (s *Scheduler) Kick(agent string) {
	s.init()
	select {
	case s.kick <- agent:
	default:
	}
}

// Wake implements Waker for the native provider.
func (s *Scheduler) Wake(_ context.Context, agent, _ string) error {
	s.Kick(agent)
	return nil
}

// Run blocks until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	s.init()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	beat := func(agent string) {
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			out, err := s.D.RunOnce(ctx, agent)
			if s.Logf != nil && (err != nil || (out.Kind != OutcomeIdle && out.Kind != OutcomeBusy)) {
				s.Logf("dispatch %s: %s %s %v", agent, out.Kind, out.Status, err)
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case agent := <-s.kick:
			beat(agent)
		case <-t.C:
			agents, err := s.Agents(ctx)
			if err != nil {
				if s.Logf != nil {
					s.Logf("dispatch: list agents: %v", err)
				}
				continue
			}
			for _, a := range agents {
				beat(a)
			}
		}
	}
}
