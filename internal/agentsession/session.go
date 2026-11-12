// Package agentsession drives the *running* interactive agent sessions — the
// agent's own TUI on the container's main TTY — and exposes them
// programmatically. This is the foundation of the OpenAI-compatible endpoint:
// the same container TTY that the DockWeave web terminal / TUI attaches to is
// reused, so the agent stays visible there and remains manually usable while
// it is also driven via the API.
package agentsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
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
	// ScreenRows/ScreenCols is the emulated TTY size used for capture.
	ScreenRows int
	ScreenCols int
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
	if cfg.ScreenRows <= 0 {
		cfg.ScreenRows = 50
	}
	if cfg.ScreenCols <= 0 {
		cfg.ScreenCols = 160
	}
	return &Manager{cfg: cfg, sessions: map[string]*Session{}}
}

// Session is one agent's live terminal stream plus its rendered screen.
type Session struct {
	conn   net.Conn
	cid    string
	docker *dockerclient.Client
	screen *Screen
	mu     sync.Mutex
	quiet  time.Duration
	max    time.Duration
}

// Prompt sends a prompt to the running agent and returns only the new rendered
// content (its response). The agent's TUI renders the raw PTY stream into a
// virtual terminal; the response is the screen delta since the prompt.
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
	s := &Session{
		conn:   conn,
		cid:    cid,
		docker: m.cfg.Docker,
		screen: NewScreen(m.cfg.ScreenRows, m.cfg.ScreenCols),
		quiet:  m.cfg.QuietPeriod,
		max:    m.cfg.MaxWait,
	}
	m.sessions[idOrName] = s
	return s, nil
}

func (s *Session) prompt(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Match the emulator to the container TTY so absolute cursor movements
	// land on the right cells.
	_ = s.docker.Resize(ctx, s.cid, s.screen.rows, s.screen.cols)

	// Absorb pending output into the screen to establish the "before" state.
	s.drainInto()
	before := s.screen.Text()

	if _, err := s.conn.Write([]byte(prompt + "\r")); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	last := time.Now()
	deadline := time.Now().Add(s.max)
	tmp := make([]byte, 4096)
	got := false
	for time.Now().Before(deadline) {
		_ = s.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		n, err := s.conn.Read(tmp)
		if n > 0 {
			s.screen.Feed(tmp[:n])
			last = time.Now()
			got = true
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if got && time.Since(last) >= s.quiet {
					break
				}
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				continue
			}
			if got {
				break
			}
			return "", err
		}
	}
	return diffScreens(before, s.screen.Text()), nil
}

func (s *Session) drainInto() {
	_ = s.conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	tmp := make([]byte, 4096)
	for {
		n, err := s.conn.Read(tmp)
		if n > 0 {
			s.screen.Feed(tmp[:n])
		}
		if err != nil {
			return
		}
	}
}

// diffScreens returns the lines of `after` that are new relative to `before`.
// It finds the longest common contiguous run of lines (the shared history /
// redraw) and returns everything after it — the prompt echo plus the response.
func diffScreens(before, after string) string {
	bl := splitLines(before)
	al := splitLines(after)
	bestLen, bestEnd := 0, 0
	for i := 0; i < len(al); i++ {
		for j := 0; j < len(bl); j++ {
			k := 0
			for i+k < len(al) && j+k < len(bl) && al[i+k] == bl[j+k] {
				k++
			}
			if k > bestLen {
				bestLen, bestEnd = k, i+k
			}
		}
	}
	return strings.TrimSpace(strings.Join(al[bestEnd:], "\n"))
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Z]|\r`)

// StripANSI removes ANSI escape sequences and carriage returns.
func StripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
