package nativeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// promptClaude types the prompt into the live Claude TUI (so the user sees it)
// and reads the answer from the structured session transcript instead of
// scraping the rendered screen.
func (m *Manager) promptClaude(ctx context.Context, idOrName, prompt string) (string, error) {
	cid, err := m.FindContainer(ctx, idOrName)
	if err != nil || cid == "" {
		return "", errors.New("agent is not running")
	}
	path, before, err := m.claudeTranscript(ctx, cid)
	if err != nil {
		return "", err
	}

	if m.TypePrompt == nil {
		return "", errors.New("claude TUI input is not configured")
	}
	if err := m.TypePrompt(ctx, idOrName, prompt); err != nil {
		return "", err
	}

	// The first turn may create the transcript only after the prompt is sent.
	if path == "" {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if p, n, err := m.claudeTranscript(ctx, cid); err == nil && p != "" {
				path, before = p, n
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if path == "" {
			return "", errors.New("claude transcript did not appear")
		}
	}

	deadline := time.Now().Add(m.Timeout)
	lastChange := time.Now()
	lastOut := ""
	for time.Now().Before(deadline) {
		out, err := m.Docker.ExecOutput(ctx, cid, []string{
			"tail", "-n", "+" + strconv.Itoa(before+1), path,
		})
		if err == nil && out != lastOut {
			lastOut = out
			lastChange = time.Now()
		}
		if text := claudeAssistantText(out); text != "" && time.Since(lastChange) >= 1200*time.Millisecond {
			return text, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %v", ErrAlreadySubmitted, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("%w: claude turn did not complete in time", ErrAlreadySubmitted)
}

// claudeTranscript returns the newest session transcript and its line count.
func (m *Manager) claudeTranscript(ctx context.Context, cid string) (string, int, error) {
	out, err := m.Docker.ExecOutput(ctx, cid, []string{
		"sh", "-c", "find /memory/.claude/projects -type f -name '*.jsonl' -printf '%T@ %p\\n' 2>/dev/null | sort -rn | head -1",
	})
	if err != nil {
		return "", 0, nil
	}
	fields := strings.Fields(firstLine(out))
	if len(fields) < 2 {
		return "", 0, nil
	}
	path := fields[1]
	beforeRaw, err := m.Docker.ExecOutput(ctx, cid, []string{"wc", "-l", path})
	if err != nil {
		return path, 0, nil
	}
	n, _ := strconv.Atoi(strings.TrimSpace(strings.Fields(beforeRaw)[0]))
	return path, n, nil
}

// claudeAssistantText concatenates text blocks of assistant messages in the
// new part of a Claude session transcript.
func claudeAssistantText(out string) string {
	var parts []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"assistant"`) {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type != "assistant" || ev.Message.Role != "assistant" {
			continue
		}
		for _, c := range ev.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, c.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
