package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
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
			{ID: "ws2", Name: "docs", HostPath: "/srv/docs", CreatedAt: now},
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
	mouseOn     bool
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

func (f *fakeTmux) enableMouse() { f.mouseOn = true }
func (f *fakeTmux) quitAll()     { f.quit = true }
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

func TestBarModeClickBelowBarsFocusesTerminal(t *testing.T) {
	ft := &fakeTmux{avail: true, inTmux: true}
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 4, tmux: ft, embedded: true, menubar: true, bottomPane: "pane1"}
	m.data = sampleData()
	_ = m.View()

	// Click in the terminal area (below the bars): focus goes to the agent.
	_, cmd := m.Update(leftClick(40, 8))
	if cmd == nil {
		t.Fatal("terminal-area click must move focus")
	}
	msg := cmd()
	if fd, ok := msg.(focusDoneMsg); !ok || fd.err != nil {
		t.Fatalf("focus result: %#v", msg)
	}
	if !ft.focused {
		t.Fatal("terminal pane must receive focus")
	}
	// Click on the menu bar must NOT steal focus into the terminal.
	ft.focused = false
	m.Update(leftClick(20, 1))
	if ft.focused {
		t.Fatal("menu clicks must stay on the bar")
	}
}

func TestAgentDetailShowsFolders(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 120, height: 30, tab: tabAgents, expanded: true}
	m.data = sampleData()
	m.data.paths = map[string]sdk.AgentPathsInfo{
		"ag1": {
			AgentID:  "ag1",
			AgentDir: "/var/lib/dockweave/agents/ag1/agent",
			MemoryID: "mem_1", MemoryDir: "/var/lib/dockweave/memories/mem_1",
			WorkspaceDir: "/var/lib/dockweave/agents/ag1/workspace",
		},
	}
	_ = m.View()
	out := m.View()
	for _, want := range []string{
		"folders", "AGENTS.md, skills", "long-term memory",
		"/workspace/proj", "/srv/proj", "(rw)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail card missing %q:\n%s", want, out)
		}
	}
	// An agent without mounts shows the hint instead of a project line.
	m.cursor = 0
	m.data.mounts = nil
	out = m.View()
	if !strings.Contains(out, "no project assigned") {
		t.Fatalf("missing no-project hint:\n%s", out)
	}
}

// ---- Setup tab: forms, folders, mouse+keyboard ----

func setupTestData() dataMsg {
	d := sampleData()
	d.grants = []*model.Grant{{
		ID: "gr1", WorkspaceID: "ws1", AgentID: "ag1",
		Mode: model.ModeRW, Status: model.GrantActive, CreatedAt: time.Now(),
	}}
	d.paths = map[string]sdk.AgentPathsInfo{
		"ag1": {AgentID: "ag1", AgentDir: "/a/agent", MemoryID: "mem_1", MemoryDir: "/m/mem_1", WorkspaceDir: "/a/ws"},
	}
	return d
}

// clickHit clicks the hotspot with the given action and runs the command.
func clickHit(t *testing.T, m *modelTUI, action string) tea.Msg {
	t.Helper()
	_ = m.View()
	for _, h := range m.form.hits {
		if h.action == action {
			_, cmd := m.Update(leftClick(h.x0+1, h.y))
			if cmd != nil {
				return cmd()
			}
			return nil
		}
	}
	t.Fatalf("hotspot %q missing", action)
	return nil
}

func TestSetupSettingsMode(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	out := m.View()
	for _, want := range []string{
		"alpha", "+ new agent", "workspaces", // agent tab chips
		"settings · alpha", "start", "stop", "remove",
		"folders", "AGENTS.md, skills", "long-term memory",
		"/workspace/proj", "/srv/proj", "unassign",
		"/workspace/docs", "/srv/docs", "assign",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings view missing %q:\n%s", want, out)
		}
	}
	// Memory stores stay hidden until [change] is pressed.
	if strings.Contains(out, "memory stores") {
		t.Fatal("store list must be collapsed by default")
	}
	m.activate(setupHit{action: "change"})
	if !strings.Contains(m.View(), "memory stores") {
		t.Fatal("change must reveal the memory stores")
	}
}

func TestSetupNewAgentMode(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	m.applyTypeDefaults()
	out := m.View()
	for _, want := range []string{"new agent", "Create", "Clear", "codex-01", "node:22-bookworm", "@openai/codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("new-agent view missing %q:\n%s", want, out)
		}
	}
}

func TestSetupWorkspacesMode(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 2
	out := m.View()
	for _, want := range []string{"workspaces", "register workspace", "register memory folder", "Register memory", "/srv/proj"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workspaces view missing %q:\n%s", want, out)
		}
	}
}

func TestSetupFormTypingAndFocus(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	m.applyTypeDefaults()

	// Click the name field via its hotspot: typing goes in.
	_ = m.View()
	var nameHit *setupHit
	for i := range m.form.hits {
		if m.form.hits[i].action == "f-name" {
			nameHit = &m.form.hits[i]
		}
	}
	if nameHit == nil {
		t.Fatal("name field hotspot missing")
	}
	m.Update(leftClick(nameHit.x0+1, nameHit.y))
	for _, r := range "x" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.HasSuffix(m.form.name.Value(), "x") {
		t.Fatalf("name field = %q", m.form.name.Value())
	}
	// Enter confirms the change; only then do cursor keys move on.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if !strings.HasSuffix(m.form.name.Value(), "x") {
		t.Fatal("typing must not reach name after moving on")
	}
	// Tab switches the menu, not the form.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab == tabSetup {
		t.Fatal("tab must switch the menu")
	}
}

func TestSetupTypeDefaultsPreFill(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	_ = m.View() // seeds codex defaults
	if got := m.form.name.Value(); got != "codex-01" {
		t.Fatalf("name default = %q, want codex-01", got)
	}
	if !strings.Contains(m.form.cmd.Value(), "@openai/codex") {
		t.Fatalf("cmd default = %q", m.form.cmd.Value())
	}
	m.cycleType(true) // -> opencode
	if got := m.form.name.Value(); got != "opencode-01" {
		t.Fatalf("name after switch = %q", got)
	}
	if !strings.Contains(m.form.cmd.Value(), "opencode-ai") {
		t.Fatalf("cmd after switch = %q", m.form.cmd.Value())
	}
	m.focusAction("f-name")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // enter = change
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	typed := m.form.name.Value()
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	m.cycleType(true)                        // -> claude
	if m.form.name.Value() != typed {
		t.Fatalf("typed name must survive, got %q want %q", m.form.name.Value(), typed)
	}
	if !strings.Contains(m.form.cmd.Value(), "@anthropic-ai/claude-code") {
		t.Fatalf("cmd after switch = %q", m.form.cmd.Value())
	}
	m.activate(setupHit{action: "clear"})
	if m.form.name.Value() != "claude-01" || !strings.Contains(m.form.cmd.Value(), "claude-code") {
		t.Fatalf("clear must restore defaults: name=%q cmd=%q", m.form.name.Value(), m.form.cmd.Value())
	}
}

func TestSetupAssignClickIssuesCommand(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = sampleData() // no grants: ws1 is unassigned
	msg := clickHit(t, m, "assign:ws1")
	if _, ok := msg.(setupDoneMsg); !ok {
		t.Fatalf("expected setupDoneMsg, got %#v", msg)
	}
}

func TestSetupUnassignRequiresActiveGrant(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	msg := clickHit(t, m, "unassign:ws1")
	if _, ok := msg.(setupDoneMsg); !ok {
		t.Fatalf("expected setupDoneMsg, got %#v", msg)
	}
	for _, h := range m.form.hits {
		if h.action == "unassign:ws2" {
			t.Fatal("ws2 has no grant and must not offer unassign")
		}
	}
}

func TestSetupAgentChipsSwitchSelection(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	clickHit(t, m, "select:ag2")
	if m.cursor != 1 || m.form.mode != 0 {
		t.Fatalf("chip click must select agent 2: cursor=%d mode=%d", m.cursor, m.form.mode)
	}
	if !strings.Contains(m.View(), "settings · beta") {
		t.Fatal("settings must follow the selection")
	}
}

func TestChromeAutoHidesForTallContent(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 12, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	out := m.View()
	if strings.Contains(out, "dynamic workspaces for isolated AI agents") {
		t.Fatalf("title must hide when content is tall:\n%s", out)
	}
	if !strings.Contains(out, "DockWeave") {
		t.Fatalf("restore strip missing:\n%s", out)
	}
	// Click on the strip brings the chrome back (forced).
	m.Update(leftClick(2, 0))
	m.height = 45
	if !strings.Contains(m.View(), "dynamic workspaces for isolated AI agents") {
		t.Fatal("menu must return after clicking the strip")
	}
}

// TestAntigravityPreset: the create form pre-fills the official installer
// for the antigravity profile (gemini-cli is legacy per Google's docs).
func TestAntigravityPreset(t *testing.T) {
	p := agentPresets["antigravity"]
	if !strings.Contains(p.command, "antigravity.google/cli/install.sh") || !strings.Contains(p.command, "agy") {
		t.Fatalf("antigravity preset must use the official installer: %q", p.command)
	}
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	_ = m.View()
	// antigravity is offered as a profile and fills the form on switch.
	found := false
	for i, tp := range agentTypes {
		if tp == "antigravity" {
			found = true
			m.form.agentType = i
		}
	}
	if !found {
		t.Fatal("agentTypes must offer antigravity")
	}
	m.applyTypeDefaults()
	if !strings.Contains(m.form.cmd.Value(), "agy") {
		t.Fatalf("antigravity defaults missing: %q", m.form.cmd.Value())
	}
}

func TestFolderDotsMeanAssigned(t *testing.T) {
	// Green dots mark active/assigned folders for all three rows alike.
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	out := stripANSI(m.View())
	for _, row := range []string{"/agent  ←", "/memory ←", "/workspace/proj"} {
		i := strings.Index(out, row)
		if i < 0 {
			t.Fatalf("row %s missing", row)
		}
		if !strings.Contains(out[max(0, i-16):i], "●") {
			t.Fatalf("assigned row %s must show a green dot:\n%s", row, out)
		}
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestSetupKeyboardInteractionModel(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 45, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	_ = m.View()

	// Cursor keys move the selection and the hint follows.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	h1 := m.form.focusedHit()
	if h1 == nil {
		t.Fatal("down must select a control")
	}
	hint := m.setupHint(h1)
	if !strings.Contains(hint, "enter:") {
		t.Fatalf("hint must show the actions: %q", hint)
	}
	// Enter starts changing the selected control (marked as editing).
	m.focusAction("f-name")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.form.editing {
		t.Fatal("enter must enter change mode")
	}
	// Esc cancels and restores the previous value.
	before := m.form.name.Value()
	if ti := m.form.focusedInput(); ti != nil {
		ti.SetValue("changed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.form.editing {
		t.Fatal("esc must leave change mode")
	}
	if ti := m.form.focusedInput(); ti != nil && ti.Value() != before {
		t.Fatalf("esc must restore the value: %q want %q", ti.Value(), before)
	}
	// The selected control is marked in the view.
	if !strings.Contains(m.View(), "▸") {
		t.Fatal("selected control must be marked")
	}
}

func TestFolderInputVisibleOnSetFolder(t *testing.T) {
	// Regression: the path input must actually be rendered and focusable
	// after [set folder] — it used to be focused while invisible.
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 50, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	_ = m.View()
	if strings.Contains(m.View(), "set folder \n") {
		t.Fatal("folder input must be closed initially")
	}
	clickHit(t, m, "folder-agent")
	out := m.View()
	if !strings.Contains(out, "folder for") || !strings.Contains(out, "Apply") {
		t.Fatalf("set-folder input must be visible after [set folder]:\n%s", out)
	}
	if m.form.focusedInput() == nil {
		t.Fatal("the folder field must be focused and editable")
	}
	// Typing goes into the path field.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !strings.HasPrefix(m.form.folder.Value(), "/") {
		t.Fatalf("folder field not editable: %q", m.form.folder.Value())
	}
}

func TestSelectedFieldIsMarked(t *testing.T) {
	m := &modelTUI{keys: defaultKeys(), width: 130, height: 50, tab: tabSetup,
		form: newSetupForm(), client: sdk.New("/tmp/nonexistent.sock")}
	m.data = setupTestData()
	m.form.mode = 1
	_ = m.View()
	m.focusAction("f-image")
	out := m.View()
	// The selected field carries the marker; unselected ones do not.
	if strings.Count(out, "▸") < 1 {
		t.Fatalf("selected field must be marked:\n%s", out)
	}
	h := m.form.focusedHit()
	if h == nil || !strings.Contains(m.setupHint(h), "enter: edit") {
		t.Fatal("hint must describe the field actions")
	}
}
