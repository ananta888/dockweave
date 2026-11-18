package nativeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const codexThreadFile = "/memory/.dockweave/codex-thread"
const codexBin = "/memory/.local/bin/codex"

var threadIDRe = regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// promptCodex injects the prompt into the TUI's thread via `codex queue` and
// reads the final assistant message from the thread rollout. The TUI shares
// the app-server daemon, so it renders the queued turn live.
func (m *Manager) promptCodex(ctx context.Context, idOrName, prompt string) (string, error) {
	cid, err := m.FindContainer(ctx, idOrName)
	if err != nil || cid == "" {
		return "", errors.New("agent is not running")
	}
	tid := ""
	if b, err := m.Docker.ExecOutput(ctx, cid, []string{"cat", codexThreadFile}); err == nil {
		tid = strings.TrimSpace(b)
	}
	if tid == "" {
		// Not started through the native entrypoint: use the TUI fallback.
		return "", errors.New("codex native thread is not pinned")
	}
	path, err := m.codexRollout(ctx, cid, tid)
	if err != nil {
		return "", err
	}
	if tid == "" {
		if m2 := threadIDRe.FindStringSubmatch(path); len(m2) == 2 {
			tid = m2[1]
		}
	}
	if tid == "" {
		return "", errors.New("codex thread id not found")
	}

	beforeRaw, err := m.Docker.ExecOutput(ctx, cid, []string{"wc", "-l", path})
	if err != nil {
		return "", fmt.Errorf("count rollout: %w", err)
	}
	before, _ := strconv.Atoi(strings.TrimSpace(strings.Fields(beforeRaw)[0]))

	if _, err := m.Docker.ExecOutput(ctx, cid, []string{
		codexBin, "queue", "--thread", tid, "--message", prompt,
	}); err != nil {
		return "", fmt.Errorf("codex queue: %w", err)
	}

	// The thread may still be busy with a previous turn. Wait until the new
	// user message appears before accepting a task_complete, otherwise a stale
	// completion from the previous turn is attributed to this prompt.
	anchor := taskAnchor(prompt)
	seenUser := false
	started := time.Now()
	deadline := time.Now().Add(m.Timeout)
	for time.Now().Before(deadline) {
		out, err := m.Docker.ExecOutput(ctx, cid, []string{
			"tail", "-n", "+" + strconv.Itoa(before+1), path,
		})
		if err == nil && strings.TrimSpace(out) != "" {
			if !seenUser && (anchor == "" || strings.Contains(out, anchor)) {
				seenUser = true
			}
			// Fallback for harnesses that do not echo the user text: after a
			// grace period accept the next completion.
			if !seenUser && time.Since(started) > 20*time.Second {
				seenUser = true
			}
			if seenUser {
				if msg, errMsg, ok := lastTaskComplete(out); ok {
					if errMsg != "" {
						return "", fmt.Errorf("%w: %s", ErrAlreadySubmitted, errMsg)
					}
					return msg, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %v", ErrAlreadySubmitted, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("%w: codex turn did not complete in time", ErrAlreadySubmitted)
}

func (m *Manager) codexRollout(ctx context.Context, cid, tid string) (string, error) {
	if tid != "" {
		out, err := m.Docker.ExecOutput(ctx, cid, []string{
			"find", "/memory/.codex/sessions", "-type", "f", "-name", "*" + tid + "*.jsonl",
		})
		if err == nil {
			if p := firstLine(out); p != "" {
				return p, nil
			}
		}
	}
	out, err := m.Docker.ExecOutput(ctx, cid, []string{
		"sh", "-c", "ls -t /memory/.codex/sessions/*/*/*/*.jsonl 2>/dev/null | head -1",
	})
	if err != nil {
		return "", fmt.Errorf("find codex rollout: %w", err)
	}
	p := firstLine(out)
	if p == "" {
		return "", errors.New("no codex session found")
	}
	return p, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func taskAnchor(prompt string) string {
	p := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if len(p) > 40 {
		p = p[:40]
	}
	return p
}

func lastTaskComplete(out string) (string, string, bool) {
	msg, errMsg, found := "", "", false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				Type             string `json:"type"`
				LastAgentMessage string `json:"last_agent_message"`
				Error            *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "event_msg" && ev.Payload.Type == "task_complete" {
			msg, errMsg, found = ev.Payload.LastAgentMessage, "", true
			if ev.Payload.Error != nil {
				errMsg = ev.Payload.Error.Message
			}
		}
	}
	return msg, errMsg, found
}
