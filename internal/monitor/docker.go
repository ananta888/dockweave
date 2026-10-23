package monitor

import (
	"context"
	"errors"
	"fmt"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/model"
)

// DockerProvider observes container state and resource usage via the Engine API.
type DockerProvider struct {
	Docker *dockerclient.Client
	Store  AgentLookup
}

// AgentLookup resolves agent IDs to registry records (for container IDs).
type AgentLookup interface {
	ContainerID(ctx context.Context, agentID string) (string, error)
}

// NewDockerProvider creates the provider.
func NewDockerProvider(cli *dockerclient.Client, lookup AgentLookup) *DockerProvider {
	return &DockerProvider{Docker: cli, Store: lookup}
}

// Name implements Provider.
func (p *DockerProvider) Name() string { return "docker" }

// Available implements Provider.
func (p *DockerProvider) Available(ctx context.Context) error {
	if p.Docker == nil {
		return errors.New("no docker client configured")
	}
	return p.Docker.Ping(ctx)
}

// Observe implements Provider: container state, CPU and memory usage.
func (p *DockerProvider) Observe(ctx context.Context, act *model.AgentActivity) error {
	cid, err := p.Store.ContainerID(ctx, act.AgentID)
	if err != nil {
		return fmt.Errorf("lookup container: %w", err)
	}
	if cid == "" {
		act.State = "absent"
		return nil
	}
	st, err := p.Docker.State(ctx, cid)
	if err != nil {
		return err
	}
	act.State = st
	if st != "running" {
		if act.Activity == model.ActivityUnknown {
			act.Activity = model.ActivityIdle
		}
		return nil
	}
	cpu, mem, err := p.Docker.StatsSample(ctx, cid)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	act.CPUPercent = cpu
	act.MemoryBytes = mem
	_, procs, err := p.Docker.Top(ctx, cid)
	if err == nil {
		act.Processes = len(procs)
	}
	return nil
}
