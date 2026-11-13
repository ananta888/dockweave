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
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ananta888/dockweave/internal/dockerclient"
)

// ErrNoResponse is returned when a prompt was submitted but no assistant
// output could be captured before the deadline (e.g. the TUI is sitting in a
// modal, the session died, or the prompt was never accepted).
var ErrNoResponse = errors.New("no response captured from agent")

// ErrSessionClosed is returned when the container TTY connection is gone.
var ErrSessionClosed = errors.New("agent session is closed")

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
		cfg.QuietPeriod = 2500 * time.Millisecond
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 3 * time.Minute
	}
	if cfg.ScreenRows <= 0 {
		cfg.ScreenRows = 100
	}
	if cfg.ScreenCols <= 0 {
		cfg.ScreenCols = 160
	}
	return &Manager{cfg: cfg, sessions: map[string]*Session{}}
}

// Session is one agent's live terminal stream plus its rendered screen.
//
// A background pump drains the attach stream for the whole lifetime of the
// session. That matters in real operation: if nobody reads the PTY, the
// agent's TUI blocks once the kernel buffer fills, and the user-visible
// terminal freezes. The pump also keeps the screen up to date between API
// calls, so a manual interaction in the TUI is reflected in the capture.
type Session struct {
	conn   net.Conn
	cid    string
	docker *dockerclient.Client
	quiet  time.Duration
	max    time.Duration

	writeMu sync.Mutex // serializes writes to the TTY (prompt + DSR replies)

	mu     sync.Mutex // serializes Prompt calls
	smu    sync.Mutex // guards screen and lastAt
	screen *Screen
	lastAt time.Time

	closed    chan struct{}
	closeOnce sync.Once
	raw       *os.File
	// dirty means the composer may hold text from a previous submit that was
	// not accepted (dropped Enter, cancelled call). Only then do we send
	// backspaces, so we never wipe text a human is typing in the TUI.
	dirty bool
}

// Prompt sends a prompt to the running agent and returns only its response.
func (m *Manager) Prompt(ctx context.Context, idOrName, prompt string) (string, error) {
	s, err := m.session(ctx, idOrName)
	if err != nil {
		return "", err
	}
	return s.prompt(ctx, prompt)
}

// Close releases a cached session (e.g. on agent stop). Safe to call twice.
func (m *Manager) Close(idOrName string) {
	m.mu.Lock()
	s := m.sessions[idOrName]
	delete(m.sessions, idOrName)
	m.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}

func (m *Manager) session(ctx context.Context, idOrName string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[idOrName]; ok {
		if s.alive() {
			return s, nil
		}
		delete(m.sessions, idOrName)
	}
	cid, err := m.cfg.FindContainer(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	if cid == "" {
		return nil, fmt.Errorf("agent %q is not running", idOrName)
	}
	// Reuse an existing session for the same container reachable under a
	// different key (name vs. id) instead of attaching twice.
	for k, s := range m.sessions {
		if s.cid == cid && s.alive() {
			m.sessions[idOrName] = s
			return s, nil
		}
		_ = k
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
		closed: make(chan struct{}),
	}
	if p := os.Getenv("DOCKWEAVE_RAW_DUMP"); p != "" {
		s.raw, _ = os.Create(p)
	}
	go s.pump()
	// Match the container TTY to the emulator once; the pump captures the
	// resulting redraw and the composer settles.
	_ = m.cfg.Docker.Resize(ctx, cid, s.screen.rows, s.screen.cols)
	m.sessions[idOrName] = s
	return s, nil
}

func (s *Session) alive() bool {
	select {
	case <-s.closed:
		return false
	default:
		return true
	}
}

// pump continuously drains the PTY stream into the virtual screen.
func (s *Session) pump() {
	buf := make([]byte, 8192)
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := s.conn.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.smu.Lock()
			s.screen.Feed(chunk)
			s.lastAt = time.Now()
			s.smu.Unlock()
			if s.raw != nil {
				_, _ = s.raw.Write(chunk)
			}
			s.answerTerminalQueries(chunk)
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			s.shutdown()
			return
		}
	}
}

// answerTerminalQueries replies to cursor-position requests (DSR). Full-screen
// TUIs (codex uses this) may wait for the terminal's answer; without it the
// UI can stall on startup or after a resize.
func (s *Session) answerTerminalQueries(chunk []byte) {
	if !strings.Contains(string(chunk), "\x1b[6n") {
		return
	}
	s.smu.Lock()
	row, col := s.screen.CursorPos()
	s.smu.Unlock()
	s.writeMu.Lock()
	_, _ = s.conn.Write([]byte(fmt.Sprintf("\x1b[%d;%dR", row+1, col+1)))
	s.writeMu.Unlock()
}

func (s *Session) shutdown() {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		if s.raw != nil {
			_ = s.raw.Close()
		}
	})
}

func (s *Session) prompt(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.alive() {
		return "", ErrSessionClosed
	}
	// Another terminal (web UI, tmux) may have changed the TTY size since the
	// last turn. Re-assert the emulator geometry so cursor addressing matches.
	if s.docker != nil {
		_ = s.docker.Resize(ctx, s.cid, s.screen.rows, s.screen.cols)
		time.Sleep(120 * time.Millisecond)
	}
	s.smu.Lock()
	baseCompletions := countCompletions(s.screen.TextWithScrollback())
	s.smu.Unlock()

	// Clear any leftover draft from a previously interrupted submit, then type
	// the prompt and submit it in a *separate* read on the harness side.
	// Clear a leftover draft only if a previous submit may have been dropped;
	// otherwise a human may be typing in the TUI right now.
	if s.dirty {
		if err := s.clearComposer(); err != nil {
			return "", err
		}
		s.dirty = false
	}
	if err := s.typePrompt(ctx, prompt); err != nil {
		return "", err
	}
	s.dirty = true
	typedAt := time.Now()
	s.waitForOutputAfter(ctx, typedAt, 3*time.Second)
	time.Sleep(150 * time.Millisecond)
	if !s.alive() {
		return "", ErrSessionClosed
	}
	s.writeMu.Lock()
	_, err := s.conn.Write([]byte("\r"))
	s.writeMu.Unlock()
	if err != nil {
		s.shutdown()
		return "", fmt.Errorf("submit prompt: %w", err)
	}

	if err := s.waitSettled(ctx, prompt, baseCompletions); err != nil {
		return "", err
	}
	s.smu.Lock()
	text := s.screen.TextWithScrollback()
	s.smu.Unlock()
	if os.Getenv("DOCKWEAVE_DEBUG_SESSION") != "" {
		fmt.Fprintf(os.Stderr, "\n=== RAW SCREEN ===\n%s\n=== END RAW ===\n", text)
	}
	out := extractAfterPrompt(text, prompt)
	if strings.TrimSpace(out) == "" {
		return "", ErrNoResponse
	}
	s.dirty = false
	return out, nil
}

// clearComposer deletes any text left in the harness input line. A submit that
// was dropped (or an aborted call) can leave a draft behind; without clearing
// it, the next prompt would be concatenated with stale text.
func (s *Session) clearComposer() error {
	s.writeMu.Lock()
	_, err := s.conn.Write([]byte(strings.Repeat("\x7f", 120)))
	s.writeMu.Unlock()
	if err != nil {
		s.shutdown()
		return fmt.Errorf("clear composer: %w", err)
	}
	time.Sleep(120 * time.Millisecond)
	return nil
}

// typePrompt writes the prompt text to the TTY. Multi-line prompts use
// bracketed paste so embedded newlines do not submit half-way.
func (s *Session) typePrompt(ctx context.Context, prompt string) error {
	text := prompt
	if strings.ContainsAny(prompt, "\r\n") {
		clean := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\r", "\n")
		text = "\x1b[200~" + strings.ReplaceAll(clean, "\n", "\r\n") + "\x1b[201~"
	}
	s.writeMu.Lock()
	_, err := s.conn.Write([]byte(text))
	s.writeMu.Unlock()
	if err != nil {
		s.shutdown()
		return fmt.Errorf("write prompt: %w", err)
	}
	return nil
}

// waitForOutputAfter blocks until the PTY produced output after t (i.e. the
// harness reacted to the typed text) or the timeout elapses. It never fails
// hard: if the harness renders differently we still press Enter.
func (s *Session) waitForOutputAfter(ctx context.Context, t time.Time, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.smu.Lock()
		last := s.lastAt
		s.smu.Unlock()
		if last.After(t) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitSettled waits until output has started and then been quiet for the
// configured period, or the deadline expires. A completion marker (the
// harness's own "done" footer) allows an early return; without one we keep
// waiting through the model's thinking gap so a slow first token is not
// mistaken for "no response".
func (s *Session) waitSettled(ctx context.Context, prompt string, baseCompletions int) error {
	start := time.Now()
	deadline := start.Add(s.max)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}
		s.smu.Lock()
		last := s.lastAt
		text := s.screen.TextWithScrollback()
		s.smu.Unlock()
		if last.Before(start) {
			// Nothing has been written since Enter. Give the harness a window
			// to react, then give up instead of waiting for MaxWait.
			if now.Sub(start) > 20*time.Second {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if now.Sub(last) >= s.quiet {
			// Finished when the harness emitted a completion footer for this
			// turn, either inside the text after the prompt echo (anchor
			// visible) or as a new footer count (anchor scrolled away).
			if countCompletions(text) > baseCompletions || hasCompletion(s.afterPrompt(text, prompt)) {
				return nil
			}
			// Fallback for harnesses without a footer — but not while the TUI
			// still displays a busy indicator.
			if now.Sub(start) >= 30*time.Second && !isBusy(text) {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// countCompletions counts harness completion footers in a screen snapshot.
func countCompletions(s string) int {
	n := 0
	for _, l := range splitLines(s) {
		if isCompletionLine(l) {
			n++
		}
	}
	return n
}

// isBusy reports whether the TUI still shows a working indicator.
func isBusy(s string) bool {
	lines := splitLines(s)
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || !containsAlnum(t) {
			continue
		}
		if strings.Contains(t, "esc to interrupt") ||
			strings.HasPrefix(t, "Working (") ||
			strings.HasPrefix(t, "Thinking") ||
			strings.HasPrefix(t, "Waiting") {
			return true
		}
		seen++
		if seen >= 8 {
			break
		}
	}
	return false
}

// hasCompletion reports whether the captured (post-prompt) text contains a
// harness completion footer, i.e. the assistant turn has finished.
func hasCompletion(after string) bool {
	for _, l := range splitLines(after) {
		t := strings.TrimSpace(l)
		if strings.Contains(t, "tok/s") || strings.HasPrefix(t, "Worked for ") {
			return true
		}
	}
	return false
}

// afterPrompt returns the raw screen text after the last prompt echo.
func (s *Session) afterPrompt(text, prompt string) string { return rawAfterPrompt(text, prompt) }

func rawAfterPrompt(screenText, prompt string) string {
	lines := splitLines(screenText)
	last := lastAnchorLine(lines, prompt)
	if last >= 0 {
		return strings.Join(lines[last+1:], "\n")
	}
	// The prompt echo scrolled out of the TUI viewport (long answer). Keep only
	// the current turn: everything after the previous turn's completion footer.
	// Returning the whole screen here would leak earlier conversation history.
	// Pick the last footer that is followed by actual answer content (a footer
	// at the very bottom is the current turn's own end marker).
	var footers []int
	for i, l := range lines {
		if isCompletionLine(l) {
			footers = append(footers, i)
		}
	}
	for k := len(footers) - 1; k >= 0; k-- {
		tail := strings.Join(lines[footers[k]+1:], "\n")
		if cleanResponse(tail) != "" {
			return tail
		}
	}
	return ""
}

func isCompletionLine(l string) bool {
	t := strings.TrimSpace(l)
	return strings.Contains(t, "tok/s") || strings.HasPrefix(t, "Worked for ")
}

func lastAnchorLine(lines []string, prompt string) int {
	anchor := promptAnchor(prompt)
	if anchor == "" {
		return -1
	}
	last := -1
	for i, l := range lines {
		if strings.Contains(l, anchor) {
			last = i
		}
	}
	return last
}

// extractAfterPrompt returns the screen content after the LAST echo of the
// prompt — i.e. the agent's response to this prompt. Anchoring on the prompt
// (rather than diffing) is robust because Docker.Attach replays no scrollback:
// the "before" screen is empty and the TUI only redraws on input.
func extractAfterPrompt(screenText, prompt string) string {
	return cleanResponse(rawAfterPrompt(screenText, prompt))
}

// cleanResponse drops opencode/codex TUI chrome from a captured response:
// box/spinner-only lines, the "+ Thought …" indicator, the "Build · … tok/s"
// footer and status-bar lines.
func cleanResponse(s string) string {
	lines := splitLines(s)
	// Drop trailing TUI status-bar lines (they sit at the bottom of the screen).
	for len(lines) > 0 && isStatusBar(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !containsAlnum(t) {
			continue // only box-drawing / spinner / whitespace
		}
		t, keep := cleanLine(t)
		if !keep {
			continue
		}
		out = append(out, t)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// cleanLine removes harness chrome from one screen line. It returns the cleaned
// line and whether the line should be kept at all.
func cleanLine(t string) (string, bool) {
	// opencode
	if strings.HasPrefix(t, "+ Thought") || strings.Contains(t, "tok/s") {
		return "", false
	}
	// codex
	if strings.HasPrefix(t, "Worked for ") ||
		strings.Contains(t, "esc to interrupt") ||
		strings.HasPrefix(t, "Ask Codex to do anything") ||
		strings.HasPrefix(t, "← for agents") ||
		strings.HasPrefix(t, "└ Tip:") ||
		strings.HasPrefix(t, "Tip: Use") ||
		strings.Contains(t, "f2 to view") ||
		strings.HasPrefix(t, "⚠ ") ||
		strings.Contains(t, "default · /workspace/") {
		return "", false
	}
	// opencode onboarding sidebar / status text
	if strings.Contains(t, "Getting started") ||
		strings.Contains(t, "OpenCode includes free models") ||
		strings.Contains(t, "Connect provider") ||
		strings.Contains(t, "Claude, GPT, Gemini") ||
		strings.HasPrefix(t, "use other models") ||
		strings.Contains(t, "start immediately") ||
		strings.Contains(t, "Build auto ·") {
		return "", false
	}
	// codex renders assistant messages with a "• " bullet and the empty
	// composer with a "› " / "❯ " prompt.
	t = strings.TrimPrefix(t, "• ")
	t = strings.TrimPrefix(t, "› ")
	t = strings.TrimPrefix(t, "❯ ")
	if t == "Ask Codex to do anything" {
		return "", false
	}
	// A TUI status bar can bleed onto an answer line at the far right (e.g.
	// "26          9 269"). Drop a trailing counter fragment separated by a
	// wide gap of spaces.
	t = statusFragmentRe.ReplaceAllString(t, "$1")
	// opencode renders a right-hand sidebar; drop what was painted into the
	// right columns of an answer line.
	t = wideGapRe.ReplaceAllString(t, "$1")
	return t, true
}

var (
	statusFragmentRe = regexp.MustCompile(`(\S)\s{4,}[0-9% ]+$`)
	wideGapRe        = regexp.MustCompile(`(\S)\s{20,}\S.*$`)
)

func containsAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func promptAnchor(prompt string) string {
	// Anchor on the LAST non-empty line of the prompt: for multi-line prompts
	// the earlier lines are part of the echoed block, so anchoring on the first
	// line would leak the remaining prompt lines into the response.
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	p := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			p = s
			break
		}
	}
	if len(p) > 40 {
		p = p[:40]
	}
	return p
}

func isStatusBar(line string) bool {
	if strings.Contains(line, "ctrl+p") {
		return true
	}
	// opencode status bar: the agent's cwd as the last line (may carry
	// padding/counters, so match on the trimmed prefix).
	return strings.HasPrefix(strings.TrimSpace(line), "/workspace/")
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
