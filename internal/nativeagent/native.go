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

// ErrAlreadySubmitted means the prompt reached the agent but reading the
// structured response failed. Callers must not fall back to the TUI driver,
// otherwise the prompt would be submitted twice.
var ErrAlreadySubmitted = errors.New("prompt already submitted to the agent")

// Manager routes prompts to native harness backends.
type Manager struct {
	Docker        *dockerclient.Client
	FindContainer func(ctx context.Context, idOrName string) (string, error)
	Timeout       time.Duration
	// TypePrompt submits a prompt into the live TUI (used by the Claude
	// transcript adapter).
	TypePrompt func(ctx context.Context, idOrName, prompt string) error
}

// New creates a Manager. Timeout defaults to 3 minutes.
func New(docker *dockerclient.Client, find func(ctx context.Context, idOrName string) (string, error)) *Manager {
	return &Manager{Docker: docker, FindContainer: find, Timeout: 3 * time.Minute}
}

// Prompt sends a prompt through the harness's native API. It returns
// ErrUnsupported for harness types without an adapter; callers should fall
// back to the TUI session driver in that case.
func (m *Manager) Prompt(ctx context.Context, agentType, runtime, idOrName, prompt string) (string, error) {
	if runtime == "herdr" {
		return m.promptHerdr(ctx, idOrName, prompt)
	}
	switch agentType {
	case "opencode":
		return m.promptOpenCode(ctx, idOrName, prompt)
	case "codex":
		return m.promptCodex(ctx, idOrName, prompt)
	case "claude":
		return m.promptClaude(ctx, idOrName, prompt)
	default:
		return "", ErrUnsupported
	}
}
