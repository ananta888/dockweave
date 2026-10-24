package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// tmuxCtl orchestrates agent terminals in tmux panes (review: show agents
// via tmux, keep the DockWeave menu bar visible above). Unit tests use a
// fake; production uses realTmux.
type tmuxCtl interface {
	// available reports whether tmux is installed.
	available() bool
	// inSession reports whether the TUI runs inside a tmux session.
	inSession() bool
	// splitAgent opens the agent terminal in a split pane below the TUI
	// and shrinks the TUI pane to the menu bar.
	splitAgent(agent, termSocket string) error
	// resizeSelf changes the height of the TUI pane.
	resizeSelf(rows int) error
	// active reports whether a split was opened by us.
	active() bool
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

func (t *realTmux) splitAgent(agent, termSocket string) error {
	sess := "dw-" + agent
	// Stable per-agent session: survives the split and can be re-attached.
	if err := tmuxRun("has-session", "-t", sess); err != nil {
		attach := fmt.Sprintf("dockweave agent attach %s --term-socket %s", agent, termSocket)
		if err := tmuxRun("new-session", "-d", "-s", sess, attach); err != nil {
			return err
		}
	}
	if err := tmuxRun("split-window", "-v", "-l", "70%", "tmux attach -t "+sess); err != nil {
		return err
	}
	t.splitOpen = true
	// Shrink our own pane to the menu bar (the terminal owns the rest).
	return t.resizeSelf(3)
}

func (t *realTmux) resizeSelf(rows int) error {
	self := t.selfPaneID()
	if self == "" {
		return fmt.Errorf("tmux: cannot resolve own pane")
	}
	err := tmuxRun("resize-pane", "-t", self, "-y", strconv.Itoa(rows))
	if err == nil && rows > 4 {
		t.splitOpen = false
	}
	return err
}

func (t *realTmux) selfPaneID() string {
	if t.selfPane != "" {
		return t.selfPane
	}
	out, err := tmuxOutput("display-message", "-p", "#{pane_id}")
	if err != nil {
		return ""
	}
	t.selfPane = strings.TrimSpace(out)
	return t.selfPane
}

func tmuxRun(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

func tmuxOutput(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return string(out), err
}
