package nativeagent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// promptHerdr submits the prompt through herdr's agent API and reads the
// answer from the Codex rollout JSONL. The TUI lives in a herdr pane, so the
// prompt and answer stay visible there.
func (m *Manager) promptHerdr(ctx context.Context, idOrName, prompt string) (string, error) {
	cid, err := m.FindContainer(ctx, idOrName)
	if err != nil || cid == "" {
		return "", errors.New("agent is not running")
	}
	nameOut, err := m.Docker.ExecOutput(ctx, cid, []string{"printenv", "DOCKWEAVE_HARNESS_NAME"})
	if err != nil {
		return "", fmt.Errorf("read herdr harness name: %w", err)
	}
	name := strings.TrimSpace(nameOut)
	if name == "" {
		return "", errors.New("herdr harness name is not set")
	}

	timeoutMS := int(m.Timeout / time.Millisecond)
	_, err = m.Docker.ExecOutput(ctx, cid, []string{
		"env", "HOME=/memory",
		"timeout", strconv.Itoa(int(m.Timeout.Seconds())),
		"/usr/local/bin/herdr", "agent", "prompt", name, prompt,
		"--wait", "--timeout", strconv.Itoa(timeoutMS),
	})
	if err != nil {
		return "", fmt.Errorf("%w: herdr prompt: %v", ErrAlreadySubmitted, err)
	}

	// `--wait` means the turn is complete. The active Codex thread is the
	// newest rollout; it may have been created by this very prompt.
	path := ""
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := m.codexRollout(ctx, cid, ""); err == nil && p != "" {
			path = p
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if path == "" {
		return "", fmt.Errorf("%w: herdr agent has no codex rollout", ErrAlreadySubmitted)
	}

	deadline = time.Now().Add(m.Timeout)
	for time.Now().Before(deadline) {
		out, err := m.Docker.ExecOutput(ctx, cid, []string{"tail", "-n", "200", path})
		if err == nil {
			if msg, errMsg, ok := lastTaskComplete(out); ok {
				if errMsg != "" {
					return "", fmt.Errorf("%w: %s", ErrAlreadySubmitted, errMsg)
				}
				return msg, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %v", ErrAlreadySubmitted, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("%w: herdr codex turn did not complete in time", ErrAlreadySubmitted)
}
