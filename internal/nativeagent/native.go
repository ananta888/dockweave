// Package nativeagent talks to agent harnesses through their own structured
// interfaces (opencode server, codex app-server, …) instead of scraping the
// rendered TUI. The existing agentsession package remains the fallback.
package nativeagent

import (
	"context"
	"errors"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
)

// ErrUnsupported means the harness type has no native backend.
var ErrUnsupported = errors.New("native backend not supported for this agent type")

// Manager routes prompts to native harness backends.
type Manager struct {
	Docker        *dockerclient.Client
	FindContainer func(ctx context.Context, idOrName string) (string, error)
	Timeout       time.Duration
}

// New creates a Manager. Timeout defaults to 3 minutes.
func New(docker *dockerclient.Client, find func(ctx context.Context, idOrName string) (string, error)) *Manager {
	return &Manager{Docker: docker, FindContainer: find, Timeout: 3 * time.Minute}
}

// Prompt sends a prompt through the harness's native API. It returns
// ErrUnsupported for harness types without an adapter; callers should fall
// back to the TUI session driver in that case.
func (m *Manager) Prompt(ctx context.Context, agentType, idOrName, prompt string) (string, error) {
	switch agentType {
	case "opencode":
		return m.promptOpenCode(ctx, idOrName, prompt)
	default:
		return "", ErrUnsupported
	}
}
