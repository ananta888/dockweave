package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ananta888/dockweave/internal/model"
)

func sampleData() dataMsg {
	now := time.Now()
	return dataMsg{
		agents: []*model.Agent{
			{ID: "ag1", Name: "alpha", Type: "codex", Image: "img-a", State: model.AgentRunning},
			{ID: "ag2", Name: "beta", Type: "custom", Image: "img-b", State: model.AgentRunning},
		},
		activity: []*model.AgentActivity{
			{AgentID: "ag1", State: "running", Activity: model.ActivityWriting, Available: true,
				CPUPercent: 3, MemoryBytes: 64 * 1024 * 1024},
		},
		workspaces: []*model.Workspace{
			{ID: "ws1", Name: "proj", HostPath: "/srv/proj", CreatedAt: now},
		},
		mounts: []*model.MountState{
			{ID: "m1", AgentID: "ag1", HostPath: "/srv/proj", TargetPath: "/var/lib/dockweave/agents/ag1/workspace/proj",
				Mode: model.ModeRW, Status: model.MountActive, UpdatedAt: now},
			{ID: "m2", AgentID: "ag2", HostPath: "/srv/docs", TargetPath: "/var/lib/dockweave/agents/ag2/workspace/docs",
				Mode: model.ModeRO, Status: model.MountActive, UpdatedAt: now},
		},
		events: []*model.Event{
			{ID: "ev1", Type: model.EventGrantCreated, Actor: "root", Message: "grant on proj", Timestamp: now},
		},
		conflicts: []model.FileConflict{
			{Path: "/srv/proj/x", AgentIDs: []string{"ag1", "ag2"}, Writers: []string{"ag1"}, Detected: now},
		},
	}
}

// TestAllViewsRenderWithoutPanic (review-008): every tab renders headless
// with sample data and shows its own content markers.
func TestAllViewsRenderWithoutPanic(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 100, height: 40}
	m.data = sampleData()
	markers := map[tab][]string{
		tabDashboard:  {"alpha", "beta", "WRITING", "file conflict"},
		tabAgents:     {"alpha", "beta", "img-a"},
		tabWorkspaces: {"proj", "/srv/proj"},
		tabMounts:     {"ag1", "/srv/proj", "mounted"},
		tabEvents:     {"grant.created", "grant on proj"},
	}
	for i := 0; i < int(tabCount); i++ {
		m.tab = tab(i)
		out := m.View()
		if out == "" {
			t.Fatalf("view %d (%s) rendered empty", i, tabNames[i])
		}
		if !strings.Contains(out, "DockWeave") {
			t.Fatalf("view %d: title missing", i)
		}
		for _, want := range markers[tab(i)] {
			if !strings.Contains(out, want) {
				t.Fatalf("view %d (%s) missing marker %q in:\n%s", i, tabNames[i], want, out)
			}
		}
	}
}

func TestErrorStateRenders(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 80, height: 24}
	m.data = dataMsg{err: errDaemonDown{}}
	out := m.View()
	if !strings.Contains(out, "daemon:") || !strings.Contains(out, "dockweaved") {
		t.Fatalf("error state not rendered: %s", out)
	}
}

type errDaemonDown struct{}

func (errDaemonDown) Error() string { return "socket unreachable" }

func TestUpdateNavigationKeys(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 80, height: 24}
	m.data = sampleData()

	keyMsg := func(k rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}} }

	// tab cycles forward, shift-tab backward.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab}); cmd != nil || m.tab != tabAgents {
		t.Fatalf("tab: tab=%v cmd=%v", m.tab, cmd)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != tabDashboard {
		t.Fatalf("shift-tab: tab=%v", m.tab)
	}
	// 'l' behaves like next, 'h' like prev.
	m.Update(keyMsg('l'))
	if m.tab != tabAgents {
		t.Fatalf("l: tab=%v", m.tab)
	}
	m.Update(keyMsg('h'))
	if m.tab != tabDashboard {
		t.Fatalf("h: tab=%v", m.tab)
	}

	// Cursor moves only in the agents tab and clamps at the edges.
	m.tab = tabAgents
	m.Update(keyMsg('j'))
	m.Update(keyMsg('j')) // clamped at last agent (len=2)
	if m.cursor != 1 {
		t.Fatalf("cursor down clamp: %d", m.cursor)
	}
	m.Update(keyMsg('k'))
	m.Update(keyMsg('k'))
	if m.cursor != 0 {
		t.Fatalf("cursor up clamp: %d", m.cursor)
	}
	m.tab = tabDashboard
	m.Update(keyMsg('j'))
	if m.cursor != 0 {
		t.Fatalf("cursor must not move outside agents tab: %d", m.cursor)
	}

	// q quits (returns a non-nil quit command).
	_, cmd := m.Update(keyMsg('q'))
	if cmd == nil {
		t.Fatal("q must return the quit command")
	}
}

func TestUpdateDataAndTickDoNotPanic(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 80, height: 24}
	// data message replaces the snapshot; tick schedules refresh+next tick.
	if _, cmd := m.Update(sampleData()); cmd != nil {
		t.Fatal("dataMsg must not schedule commands")
	}
	if len(m.data.agents) != 2 {
		t.Fatal("dataMsg not applied")
	}
	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick must schedule the next tick")
	}
	// Window sizing works headless.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	if m.width != 120 {
		t.Fatalf("width=%d", m.width)
	}
	_ = m.View() // must not panic after updates
}

// ---- mouse, compact details, menubar mode (tmux) ----

type fakeTmux struct {
	avail       bool
	inTmux      bool
	splits      []string
	resized     []int
	switched    []string
	focused     bool
	selfFocused bool
	quit        bool
	bottom      string
	setupErr    error
}

func (f *fakeTmux) available() bool { return f.avail }
func (f *fakeTmux) inSession() bool { return f.inTmux }
func (f *fakeTmux) active() bool    { return len(f.resized) > 0 && f.resized[len(f.resized)-1] <= 5 }
func (f *fakeTmux) setupEmbedded(placeholder string) (string, error) {
	if f.setupErr != nil {
		return "", f.setupErr
	}
	f.bottom = "pane1"
	_ = f.resizeSelf(4)
	return "pane1", nil
}
func (f *fakeTmux) switchTerminal(pane, agent, termSocket string) error {
	f.switched = append(f.switched, agent)
	return nil
}
func (f *fakeTmux) focusTerminal(pane string) error {
	f.focused = true
	return nil
}

func (f *fakeTmux) focusSelf() { f.selfFocused = true }
func (f *fakeTmux) quitAll()   { f.quit = true }
func (f *fakeTmux) splitAgent(agent, termSocket string) error {
	f.splits = append(f.splits, agent)
	return f.resizeSelf(3)
}
func (f *fakeTmux) resizeSelf(rows int) error {
	f.resized = append(f.resized, rows)
	return nil
}

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg(tea.MouseEvent{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

func TestMenuBarClickSwitchesTabs(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 100, height: 30}
	m.data = sampleData()
	_ = m.View() // computes tab spans
	// Click on the "Agents" tab (second span).
	var target span
	for _, s := range m.tabSpans {
		if s.tab == tabAgents {
			target = s
		}
	}
	m.Update(leftClick(target.x0+1, 1))
	if m.tab != tabAgents {
		t.Fatalf("click on Agents tab: tab=%v", m.tab)
	}
	// Click on the "Events" span switches again.
	for _, s := range m.tabSpans {
		if s.tab == tabEvents {
			target = s
		}
	}
	m.Update(leftClick(target.x0+1, 1))
	if m.tab != tabEvents {
		t.Fatalf("click on Events tab: tab=%v", m.tab)
	}
	// A click outside any span keeps the tab.
	m.Update(leftClick(95, 1))
	if m.tab != tabEvents {
		t.Fatalf("click on empty menu space changed tab: %v", m.tab)
	}
}

func TestListClickSelectsAndTogglesDetails(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 100, height: 30, tab: tabAgents}
	m.data = sampleData()
	_ = m.View() // computes listTop

	// Click the second row: select + expand.
	m.Update(leftClick(5, m.listTop+1))
	if m.cursor != 1 || !m.expanded {
		t.Fatalf("row click: cursor=%d expanded=%v", m.cursor, m.expanded)
	}
	// Click the selected row again: collapse.
	m.Update(leftClick(5, m.listTop+1))
	if m.expanded {
		t.Fatal("second click must collapse the detail card")
	}
	// Compact default: no detail card rendered when collapsed.
	if strings.Contains(m.View(), "  id ") {
		t.Fatal("collapsed view must stay compact")
	}
	m.expanded = true
	if !strings.Contains(m.View(), "  id ") {
		t.Fatal("expanded view must show the detail card")
	}
}

func TestMenubarModeRendersOnlyBar(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 100, height: 3, tmux: &fakeTmux{avail: true, inTmux: true}}
	m.data = sampleData()
	m.menubar = true
	out := m.View()
	if strings.Contains(out, "NAME") || strings.Contains(out, "AGENT") {
		t.Fatalf("menubar mode must not render content:\n%s", out)
	}
	if !strings.Contains(out, "tmux owns the terminal below") {
		t.Fatalf("menubar hint missing:\n%s", out)
	}
	// Shrink detection: a tiny window turns menubar mode on.
	m.menubar = false
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 3})
	if !m.menubar {
		t.Fatal("height <= 4 must enable menubar mode")
	}
}

func TestOpenTerminalUsesTmuxSplit(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 100, height: 30, tab: tabAgents, tmux: ft, socket: "/run/x.sock"}
	m.data = sampleData()
	m.cursor = 1
	cmd := m.openTerminal()
	if cmd != nil {
		t.Fatal("tmux split must not exec a full-screen attach")
	}
	if len(ft.splits) != 1 || ft.splits[0] != "beta" {
		t.Fatalf("split for wrong agent: %v", ft.splits)
	}
	if !m.menubar {
		t.Fatal("TUI must drop to menu-bar-only after the split")
	}
	// Esc restores the full TUI.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.menubar {
		t.Fatal("esc must leave menubar mode")
	}
}

func TestAgentTabsShowRunningWithWorkspaceLabels(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30, tab: tabAgents}
	m.data = sampleData()
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("view too short:\n%s", out)
	}
	tabs := lines[2]
	if !strings.Contains(tabs, "alpha:proj") || !strings.Contains(tabs, "beta:docs") {
		t.Fatalf("agent tabs must show agent:workspace labels:\n%s", tabs)
	}
	// Stopped agents get no tab.
	m.data.agents[1].State = model.AgentExited
	out = m.View()
	tabs = strings.Split(out, "\n")[2]
	if strings.Contains(tabs, "beta") {
		t.Fatalf("stopped agents must not appear as tabs:\n%s", tabs)
	}
	if !strings.Contains(tabs, "alpha:proj") {
		t.Fatalf("running agent tab missing:\n%s", tabs)
	}
}

func TestAgentTabClickSwitchesActiveAgent(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30}
	m.data = sampleData()
	_ = m.View() // computes agent tab spans
	if len(m.agentSpans) != 2 || len(m.agentRows) != 2 {
		t.Fatalf("expected 2 agent tabs, got %d", len(m.agentSpans))
	}
	// Click the second agent tab (beta): selects it and expands details.
	s := m.agentSpans[1]
	m.Update(leftClick(s.x0+1, 2))
	if m.tab != tabAgents {
		t.Fatal("agent tab click must switch to the agents view")
	}
	if m.cursor != m.agentRows[1] || m.cursor != 1 {
		t.Fatalf("agent tab click must select the agent: cursor=%d", m.cursor)
	}
	if !m.expanded {
		t.Fatal("agent tab click must expand the details")
	}
	// The first tab follows the selection highlight.
	if !strings.Contains(strings.Split(m.View(), "\n")[2], "beta:docs") {
		t.Fatal("active agent tab must be rendered")
	}
	s = m.agentSpans[0]
	m.Update(leftClick(s.x0+1, 2))
	if m.cursor != 0 {
		t.Fatalf("switching back failed: cursor=%d", m.cursor)
	}
}

// ---- embedded tmux layout (menu bar + agent terminal below) ----

func TestEmbeddedSetupSwitchesToFirstRunningAgent(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30, tmux: ft, embedded: true}
	m.data = sampleData()

	_, cmd := m.Update(embeddedReadyMsg{pane: "pane1"})
	if m.bottomPane != "pane1" {
		t.Fatalf("bottom pane not recorded: %q", m.bottomPane)
	}
	if cmd == nil {
		t.Fatal("first running agent must be shown immediately")
	}
	msg := cmd()
	sd, ok := msg.(switchDoneMsg)
	if !ok || sd.err != nil {
		t.Fatalf("switch result: %#v", msg)
	}
	if len(ft.switched) != 1 || ft.switched[0] != "alpha" {
		t.Fatalf("expected switch to first running agent, got %v", ft.switched)
	}
}

func TestEmbeddedTabClickSwitchesTerminal(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30, tmux: ft, embedded: true}
	m.data = sampleData()
	_, cmd := m.Update(embeddedReadyMsg{pane: "pane1"})
	if cmd != nil {
		cmd()
	}
	_ = m.View() // compute tab spans

	s := m.agentSpans[1] // beta tab
	_, cmd = m.Update(leftClick(s.x0+1, 2))
	if cmd != nil {
		if sd, ok := cmd().(switchDoneMsg); !ok || sd.err != nil {
			t.Fatalf("switch failed: %#v", sd)
		} else {
			m.Update(sd) // deliver the result like bubbletea would
		}
	}
	if len(ft.switched) != 2 || ft.switched[1] != "beta" {
		t.Fatalf("tab click must switch the terminal pane: %v", ft.switched)
	}
	if m.cursor != 1 {
		t.Fatalf("cursor must follow: %d", m.cursor)
	}
	// Agent selected = compact: only menu + agent tabs on top.
	if !m.menubar {
		t.Fatal("selecting an agent must collapse the bar to menu + tabs")
	}
	// Clicking the same tab again must not rebuild the terminal session.
	_, cmd = m.Update(leftClick(s.x0+1, 2))
	if cmd != nil {
		cmd()
	}
	if len(ft.switched) != 2 {
		t.Fatalf("same-tab click must not churn the session: %v", ft.switched)
	}
	// 't' focuses the terminal.
	_, tcmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if tcmd != nil {
		tcmd()
	}
	if !ft.focused {
		t.Fatal("t must move focus into the terminal pane")
	}
}

func TestEmbeddedQuitKillsSession(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30, tmux: ft, embedded: true}
	m.data = sampleData()
	_, cmd := m.Update(embeddedReadyMsg{pane: "pane1"})
	if cmd != nil {
		cmd()
	}
	_, quit := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quit == nil {
		t.Fatal("q must quit")
	}
	if !ft.quit {
		t.Fatal("q must tear the tmux session down")
	}
}

func TestBarModeShowsMenuAndAgentTabs(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 4, tmux: &fakeTmux{}, embedded: true, menubar: true}
	m.data = sampleData()
	out := m.View()
	if strings.Contains(out, "NAME") {
		t.Fatalf("bar mode must not render content lists:\n%s", out)
	}
	if !strings.Contains(out, "alpha:proj") {
		t.Fatalf("agent tabs must stay visible in bar mode:\n%s", out)
	}
}

func TestMenuBarClickExpandsFromBarMode(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 4, tmux: ft, embedded: true, menubar: true}
	m.data = sampleData()
	_ = m.View()

	var target span
	for _, s := range m.tabSpans {
		if s.tab == tabMounts {
			target = s
		}
	}
	m.Update(leftClick(target.x0+1, 1))
	if m.menubar {
		t.Fatal("menu click must expand the TUI so the content is visible")
	}
	if m.tab != tabMounts {
		t.Fatalf("menu click must switch the view: %v", m.tab)
	}
	if len(ft.resized) == 0 || ft.resized[len(ft.resized)-1] <= 5 {
		t.Fatalf("pane must grow, resize log: %v", ft.resized)
	}
	// esc collapses back to the bar.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.menubar {
		t.Fatal("esc must collapse back to the menu bar")
	}
}
