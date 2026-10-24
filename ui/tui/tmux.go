package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// tmuxCtl orchestrates agent terminals in tmux panes. The "embedded" mode
// runs the DockWeave menu bar in a small top pane while tmux shows the
// agent terminal below — one window, tabs switch the terminal. Unit tests
// use a fake; production uses realTmux.
type tmuxCtl interface {
	// available reports whether tmux is installed.
	available() bool
	// inSession reports whether the TUI runs inside a tmux session.
	inSession() bool
	// active reports whether we manage an embedded layout.
	active() bool
	// setupEmbedded splits the current window: the TUI shrinks to the top
	// bar and the returned pane below hosts the agent terminal.
	setupEmbedded(placeholder string) (string, error)
	// switchTerminal respawns the bottom pane with the agent's session.
	switchTerminal(pane, agent, termSocket string) error
	// focusTerminal moves keyboard focus to the terminal pane.
	focusTerminal(pane string) error
	// focusSelf keeps keyboard focus on the TUI pane (menu bar).
	focusSelf()
	// enableMouse turns on tmux mouse support for our session so clicks
	// select panes (agent terminal <-> menu bar).
	enableMouse()
	// quitAll tears the managed session down (embedded mode).
	quitAll()
	// resizeSelf changes the height of the TUI pane.
	resizeSelf(rows int) error
	// splitAgent opens an agent terminal as a split on demand (non-embedded).
	splitAgent(agent, termSocket string) error
}

// realTmux drives the tmux(1) CLI.
type realTmux struct {
	selfPane  string
	splitOpen bool
}

func (t *realTmux) available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func (t *realTmux) inSession() bool { return os.Getenv("TMUX") != "" }

func (t *realTmux) active() bool { return t.splitOpen }

// agentCmd returns the command hosting one agent terminal in a session.
// It uses the running binary so TUI and terminal client never diverge.
func agentCmd(agent, termSocket string) string {
	self, err := os.Executable()
	if err != nil {
		self = "dockweave"
	}
	return fmt.Sprintf("%s agent attach %s --term-socket %s", self, agent, termSocket)
}

func (t *realTmux) setupEmbedded(placeholder string) (string, error) {
	out, err := tmuxOutput("split-window", "-v", "-l", "70%", "-P", "-F", "#{pane_id}", placeholder)
	if err != nil {
		return "", fmt.Errorf("tmux split: %w", err)
	}
	pane := strings.TrimSpace(out)
	t.splitOpen = true
	if err := t.resizeSelf(4); err != nil {
		return pane, err
	}
	// Keep focus on the menu bar: keys must not leak into the agent UI.
	t.focusSelf()
	// Mouse clicks select panes: click the terminal to type there, click
	// the bar to come back.
	t.enableMouse()
	return pane, nil
}

func (t *realTmux) switchTerminal(pane, agent, termSocket string) error {
	// The pane runs the attach client directly — no intermediate session,
	// no nested tmux attach (which needs $TMUX unset and races a
	// kill/recreate against the display). The agent's UI keeps running in
	// the container; only the viewer restarts.
	return tmuxRun("respawn-pane", "-k", "-t", pane, agentCmd(agent, termSocket))
}

func (t *realTmux) focusTerminal(pane string) error {
	return tmuxRun("select-pane", "-t", pane)
}

func (t *realTmux) focusSelf() {
	if self := t.selfPaneID(); self != "" {
		_ = tmuxRun("select-pane", "-t", self)
	}
}

func (t *realTmux) enableMouse() {
	self := t.selfPaneID()
	if self == "" {
		return
	}
	// Session-scoped: never touch the user's other tmux sessions.
	if sess, err := tmuxOutput("display-message", "-p", "-t", self, "#{session_name}"); err == nil {
		_ = tmuxRun("set-option", "-t", strings.TrimSpace(sess), "mouse", "on")
	}
}

func (t *realTmux) quitAll() {
	_ = tmuxRun("kill-session")
	t.splitOpen = false
}

func (t *realTmux) splitAgent(agent, termSocket string) error {
	if err := tmuxRun("split-window", "-v", "-l", "70%", agentCmd(agent, termSocket)); err != nil {
		return err
	}
	t.splitOpen = true
	// Shrink our own pane to the menu bar (the terminal owns the rest).
	return t.resizeSelf(4)
}

func (t *realTmux) resizeSelf(rows int) error {
	self := t.selfPaneID()
	if self == "" {
		return fmt.Errorf("tmux: cannot resolve own pane")
	}
	err := tmuxRun("resize-pane", "-t", self, "-y", strconv.Itoa(rows))
	if err == nil && rows > 5 {
		t.splitOpen = false
	}
	return err
}

func (t *realTmux) selfPaneID() string {
	if t.selfPane != "" {
		return t.selfPane
	}
	// Inside a pane $TMUX_PANE is authoritative: display-message would
	// report the most recently used pane, not ours.
	if p := os.Getenv("TMUX_PANE"); p != "" {
		t.selfPane = p
		return p
	}
	out, err := tmuxOutput("display-message", "-p", "#{pane_id}")
	if err != nil {
		return ""
	}
	t.selfPane = strings.TrimSpace(out)
	return t.selfPane
}

func tmuxRun(args ...string) error {
	err := exec.Command("tmux", args...).Run()
	tmuxDebug(args, err)
	return err
}

func tmuxOutput(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	tmuxDebug(args, err)
	return string(out), err
}

// tmuxDebug logs every tmux invocation when DOCKWEAVE_DEBUG is set.
func tmuxDebug(args []string, err error) {
	if os.Getenv("DOCKWEAVE_DEBUG") == "" {
		return
	}
	f, ferr := os.OpenFile("/tmp/tmux-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "tmux %v -> %v (TMUX_PANE=%s)\n", args, err, os.Getenv("TMUX_PANE"))
}

// launchLayout starts the embedded layout: inside tmux the current window
// is re-used, outside tmux a managed "dockweave" session is created and
// attached. Returns false when tmux is unavailable (plain TUI fallback).
func LaunchLayout(socketPath string) (bool, error) {
	rt := &realTmux{}
	if !rt.available() {
		return false, nil
	}
	if rt.inSession() {
		return true, Run(socketPath, true)
	}
	self, err := os.Executable()
	if err != nil {
		self = "dockweave"
	}
	cmd := exec.Command("tmux", "new-session", "-A", "-s", "dockweave", self+" tui --embedded")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return true, fmt.Errorf("tmux session: %w", err)
	}
	return true, nil
}
