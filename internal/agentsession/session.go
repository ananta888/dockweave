// Package agentsession drives the *running* interactive agent sessions — the
// agent's own TUI on the container's main TTY — and exposes them
// programmatically. This is the foundation of the OpenAI-compatible endpoint:
// the same container TTY that the DockWeave web terminal / TUI attaches to is
// reused, so the agent stays visible there and remains manually usable while
// it is also driven via the API.
package agentsession

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
)

// Config configures the Manager.
type Config struct {
	Docker        *dockerclient.Client
	FindContainer func(ctx context.Context, idOrName string) (string, error)
	// QuietPeriod is how long the stream must stay silent after output before
	// a response is considered complete.
	QuietPeriod time.Duration
	// MaxWait bounds a single Prompt call.
	MaxWait time.Duration
}

// Manager drives running agent sessions.
type Manager struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*Session
}

// New creates a Manager.
func New(cfg Config) *Manager {
	if cfg.QuietPeriod <= 0 {
		cfg.QuietPeriod = 1500 * time.Millisecond
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 2 * time.Minute
	}
	return &Manager{cfg: cfg, sessions: map[string]*Session{}}
}

// Session is one agent's live terminal stream.
type Session struct {
	conn  net.Conn
	mu    sync.Mutex
	quiet time.Duration
	max   time.Duration
}

// Prompt sends a prompt to the running agent and returns its response with
// ANSI escapes stripped. Prompt calls on the same agent are serialized; the
// TUI may still write concurrently (the shared TTY echoes both).
func (m *Manager) Prompt(ctx context.Context, idOrName, prompt string) (string, error) {
	s, err := m.session(ctx, idOrName)
	if err != nil {
		return "", err
	}
	return s.prompt(ctx, prompt)
}

func (m *Manager) session(ctx context.Context, idOrName string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[idOrName]; ok {
		return s, nil
	}
	cid, err := m.cfg.FindContainer(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if cid == "" {
		return nil, fmt.Errorf("agent %q is not running", idOrName)
	}
	stream, err := m.cfg.Docker.Attach(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("attach to agent %q: %w", idOrName, err)
	}
	conn, ok := stream.(net.Conn)
	if !ok {
		stream.Close()
		return nil, errors.New("attach stream does not support deadlines")
	}
	s := &Session{conn: conn, quiet: m.cfg.QuietPeriod, max: m.cfg.MaxWait}
	m.sessions[idOrName] = s
	return s, nil
}

func (s *Session) prompt(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Drop stale output so we only capture this response.
	s.drain()

	if _, err := s.conn.Write([]byte(prompt + "\r")); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	var buf bytes.Buffer
	last := time.Now()
	deadline := time.Now().Add(s.max)
	tmp := make([]byte, 4096)
	for time.Now().Before(deadline) {
		_ = s.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		n, err := s.conn.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			last = time.Now()
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if buf.Len() > 0 && time.Since(last) >= s.quiet {
					break
				}
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				continue
			}
			// Real error/EOF: return what we have, or fail if empty.
			if buf.Len() > 0 {
				break
			}
			return "", err
		}
	}
	return StripANSI(buf.String()), nil
}

func (s *Session) drain() {
	_ = s.conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	tmp := make([]byte, 4096)
	for {
		if _, err := s.conn.Read(tmp); err != nil {
			return
		}
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Z]|\r`)

// StripANSI removes ANSI escape sequences and carriage returns.
func StripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
