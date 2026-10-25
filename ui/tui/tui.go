// Package tui implements the DockWeave terminal UI (Bubble Tea).
// All displayed data comes from the daemon API — never fabricated.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	tabActive  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("12")).Padding(0, 1)
	tabIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Padding(0, 1)
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type tab int

const (
	tabDashboard tab = iota
	tabAgents
	tabWorkspaces
	tabMounts
	tabEvents
	tabSetup
	tabCount
)

var tabNames = []string{"Dashboard", "Agents", "Workspaces", "Mounts", "Events", "Setup"}

type dataMsg struct {
	agents     []*model.Agent
	activity   []*model.AgentActivity
	workspaces []*model.Workspace
	mounts     []*model.MountState
	events     []*model.Event
	conflicts  []model.FileConflict
	grants     []*model.Grant
	memories   []sdk.MemoryInfo
	paths      map[string]sdk.AgentPathsInfo // agent id -> folder assignments
	err        error
}

type tickMsg time.Time

// termDoneMsg reports the outcome of a full-screen terminal attach.
type termDoneMsg struct{ err error }

// span is a clickable region on the menu bar (plain-text columns).
type span struct {
	tab    tab
	x0, x1 int
}

type modelTUI struct {
	client *sdk.Client
	tab    tab
	data   dataMsg
	width  int
	height int
	keys   keyMap
	cursor int    // selection in agents view
	socket string // daemon term socket (for terminals)

	// compact UI: the detail card is only shown for the selected agent.
	expanded bool
	// status is a transient message line (e.g. terminal attach errors).
	status string
	// actionHits: clickable lifecycle buttons of the Agents view.
	actionHits []setupHit
	// form state of the Setup tab.
	form setupForm
	// menubar mode: only the menu bar is rendered (tmux hosts the
	// agent terminal below; see tmux.go).
	menubar bool
	// embedded: the TUI manages the tmux layout (menu bar + terminal pane).
	embedded    bool
	bottomPane  string
	bottomAgent string // agent currently shown in the terminal pane
	// content windowing: content taller than the pane is scrolled, never
	// allowed to cover the menu. contentTop is the screen row of the first
	// content line; scrollY indexes the full content.
	scrollY    int
	contentTop int
	// click geometry, recomputed on every render.
	tabSpans []span
	// agent tabs (running agents only, label "agent:workspace").
	agentSpans []span
	agentRows  []int // agent index per tab
	listTop    int   // y of the first clickable list row
	tmux       tmuxCtl
}

type keyMap struct {
	Quit, Next, Prev, Refresh  key.Binding
	CursorUp, CursorDown, Term key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Next:       key.NewBinding(key.WithKeys("tab", "l"), key.WithHelp("tab", "next view")),
		Prev:       key.NewBinding(key.WithKeys("shift+tab", "h"), key.WithHelp("shift+tab", "prev view")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		CursorUp:   key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑", "select agent")),
		CursorDown: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "select agent")),
		Term:       key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "open terminal (agents view)")),
	}
}

// Run starts the TUI against the daemon at socketPath. With embedded=true
// the TUI builds the tmux layout: menu bar on top, agent terminal below
// (see tmux.go / launchLayout).
func Run(socketPath string, embedded bool) error {
	termSocket := filepath.Join(filepath.Dir(socketPath), "dockweaved.term.sock")
	m := &modelTUI{client: sdk.New(socketPath), keys: defaultKeys(), socket: termSocket,
		tmux: &realTmux{}, embedded: embedded, form: newSetupForm()}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// embeddedReadyMsg reports the bottom pane created for the layout.
type embeddedReadyMsg struct {
	pane string
	err  error
}

// switchDoneMsg reports the outcome of switching the terminal pane.
type switchDoneMsg struct {
	agent string
	err   error
}

// focusDoneMsg reports the outcome of moving focus into the terminal.
type focusDoneMsg struct{ err error }

// focusBottom moves keyboard focus into the agent terminal pane.
func (m *modelTUI) focusBottom() tea.Cmd {
	pane := m.bottomPane
	return func() tea.Msg {
		if err := m.tmux.focusTerminal(pane); err != nil {
			return focusDoneMsg{err: err}
		}
		return focusDoneMsg{}
	}
}

func (m *modelTUI) Init() tea.Cmd {
	if m.embedded {
		return tea.Batch(m.refresh(), tick(), m.setupEmbedded())
	}
	return tea.Batch(m.refresh(), tick())
}

// setupEmbedded creates the terminal pane below the TUI.
func (m *modelTUI) setupEmbedded() tea.Cmd {
	return func() tea.Msg {
		pane, err := m.tmux.setupEmbedded(
			`sh -c 'echo " dockweave: no agent terminal yet"; sleep infinity'`)
		return embeddedReadyMsg{pane: pane, err: err}
	}
}

// expandTUI grows the TUI pane so the selected view is visible.
func (m *modelTUI) expandTUI() {
	m.menubar = false
	_ = m.tmux.resizeSelf(m.height + 20)
}

// collapseToBar shrinks the TUI to the menu bar (terminal below owns the
// screen).
func (m *modelTUI) collapseToBar() {
	m.menubar = true
	_ = m.tmux.resizeSelf(4)
}

// switchBottom shows the selected agent's terminal in the bottom pane.
// With focus it also moves keyboard focus into the terminal. Switching to
// the agent that is already shown does not rebuild the session (no churn);
// on failure the switch stays retryable (bottomAgent is set only on success).
func (m *modelTUI) switchBottom(focus bool) tea.Cmd {
	if m.cursor >= len(m.data.agents) {
		return nil
	}
	a := m.data.agents[m.cursor]
	pane, agent, sock := m.bottomPane, a.Name, m.socket
	if m.bottomAgent == agent && !m.tmux.paneDead(pane) {
		// Already showing this agent in a live pane: no respawn needed.
		if focus {
			return func() tea.Msg {
				if err := m.tmux.focusTerminal(pane); err != nil {
					return switchDoneMsg{agent: agent, err: err}
				}
				return switchDoneMsg{agent: agent}
			}
		}
		return nil
	}
	return func() tea.Msg {
		if err := m.tmux.switchTerminal(pane, agent, sock); err != nil {
			return switchDoneMsg{agent: agent, err: err}
		}
		if focus {
			if err := m.tmux.focusTerminal(pane); err != nil {
				return switchDoneMsg{agent: agent, err: err}
			}
		}
		return switchDoneMsg{agent: agent}
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *modelTUI) refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var d dataMsg
		d.agents, d.err = m.client.ListAgents(ctx)
		if d.err != nil {
			return d
		}
		d.activity, d.err = m.client.Activity(ctx)
		if d.err != nil {
			return d
		}
		d.workspaces, d.err = m.client.ListWorkspaces(ctx)
		if d.err != nil {
			return d
		}
		d.mounts, d.err = m.client.ListMounts(ctx, "")
		if d.err != nil {
			return d
		}
		d.events, d.err = m.client.Events(ctx, 30)
		if d.err != nil {
			return d
		}
		conf, err := m.client.Conflicts(ctx)
		if err == nil {
			for _, c := range conf {
				d.conflicts = append(d.conflicts, *c)
			}
		}
		if mems, err := m.client.ListMemories(ctx); err == nil {
			d.memories = mems
		}
		if gs, err := m.client.ListGrants(ctx); err == nil {
			d.grants = gs
		}
		d.paths = map[string]sdk.AgentPathsInfo{}
		for _, a := range d.agents {
			if p, err := m.client.AgentPaths(ctx, a.ID); err == nil {
				d.paths[a.ID] = *p
			}
		}
		d.err = nil
		return d
	}
}

func (m *modelTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// A very small pane means the tmux split owns the terminal:
		// stay menu-bar-only until the pane is resized back.
		if m.height <= 4 {
			m.menubar = true
		} else if m.menubar && !m.tmux.active() {
			m.menubar = false
		}
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		// Typing context: while a Setup field is edited (or focused and a
		// printable key arrives) every character belongs to the field —
		// global single-letter bindings (r refresh, l/h menu) must not
		// swallow typing (e.g. "researcher").
		if m.tab == tabSetup && m.form.focusedInput() != nil &&
			(m.form.editing || isPrintableKey(msg)) {
			switch msg.String() {
			case "tab", "shift+tab", "pgup", "pgdown":
			default:
				return m.updateSetup(msg)
			}
		}
		switch {
		case msg.String() == "pgup":
			m.scrollY -= 10
			return m, nil
		case msg.String() == "pgdown":
			m.scrollY += 10
			return m, nil
		case key.Matches(msg, m.keys.Quit):
			if m.tab == tabSetup && m.form.editing {
				// typing context: q is text, not quit
			} else {
				if m.embedded {
					m.tmux.quitAll()
				}
				return m, tea.Quit
			}
		case key.Matches(msg, m.keys.Next):
			m.tab = (m.tab + 1) % tabCount
			m.expanded = false
			return m, nil
		case key.Matches(msg, m.keys.Prev):
			m.tab = (m.tab - 1 + tabCount) % tabCount
			m.expanded = false
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			return m, m.refresh()
		case key.Matches(msg, m.keys.CursorUp) && m.tab == tabAgents && !m.menubar:
			if m.cursor > 0 {
				m.cursor--
			}
			m.ensureRowVisible(m.listTop + m.cursor)
		case key.Matches(msg, m.keys.CursorDown) && m.tab == tabAgents && !m.menubar:
			if m.cursor < len(m.data.agents)-1 {
				m.cursor++
			}
			m.ensureRowVisible(m.listTop + m.cursor)
		case key.Matches(msg, m.keys.Term) && m.tab == tabAgents:
			return m, m.openTerminal()
		case msg.String() == "s" && m.tab == tabAgents && !m.menubar:
			return m, m.lifecycle("start")
		case msg.String() == "x" && m.tab == tabAgents && !m.menubar:
			return m, m.lifecycle("stop")
		case m.tab == tabSetup:
			return m.updateSetup(msg)
		case msg.String() == "enter" && m.tab == tabAgents && !m.menubar:
			// Toggle the detail card (compact by default).
			m.expanded = !m.expanded
		case msg.String() == "esc":
			if m.menubar {
				m.expandTUI()
				return m, nil
			}
			if m.embedded {
				m.collapseToBar()
				return m, nil
			}
			m.expanded = false
		}
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case embeddedReadyMsg:
		if msg.err != nil {
			m.status = "tmux layout: " + msg.err.Error()
			break
		}
		m.bottomPane = msg.pane
		// Show the first running agent immediately.
		for i, a := range m.data.agents {
			if a.State == model.AgentRunning {
				m.cursor = i
				return m, m.switchBottom(false)
			}
		}
	case switchDoneMsg:
		if msg.err != nil {
			// Keep bottomAgent empty so the next refresh retries.
			m.status = "terminal switch: " + msg.err.Error()
		} else {
			m.bottomAgent = msg.agent
			m.status = ""
		}
	case focusDoneMsg:
		if msg.err != nil {
			m.status = "focus terminal: " + msg.err.Error()
		}
	case setupDoneMsg:
		if msg.err != nil {
			m.status = "setup: " + msg.err.Error()
		} else {
			m.status = msg.what
			if m.status == "" {
				m.status = "done"
			}
		}
		return m, m.refresh()
	case termDoneMsg:
		if msg.err != nil {
			m.status = "terminal: " + msg.err.Error()
		} else {
			m.status = "terminal closed"
		}
	case dataMsg:
		m.data = msg
		// Embedded layout: show the first running agent as soon as data
		// arrives (the layout may be ready before the first refresh).
		// Only running agents: a stopped one must not respawn-loop.
		if m.embedded && m.bottomPane != "" && m.bottomAgent == "" {
			for i, a := range m.data.agents {
				if a.State == model.AgentRunning {
					m.cursor = i
					return m, m.switchBottom(false)
				}
			}
		}
		// A dead pane for the selected running agent gets repaired.
		if m.embedded && m.bottomPane != "" && m.bottomAgent != "" && m.tmux.paneDead(m.bottomPane) {
			if a := m.selectedAgent(); a != nil && a.State == model.AgentRunning {
				m.bottomAgent = ""
				return m, m.switchBottom(false)
			}
		}
	}
	return m, nil
}

// handleMouse maps left-button clicks on the menu bar and the agent list.
func (m *modelTUI) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Mouse wheel scrolls the content window.
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp {
		m.scrollY -= 3
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown {
		m.scrollY += 3
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	// Content clicks are matched in content coordinates (the content may
	// be scrolled; the chrome rows above use screen coordinates).
	contentY := msg.Y - m.contentTop + m.scrollY
	// Menu bar: a click reveals the view — in bar mode the TUI expands
	// first so the content is actually visible.
	if msg.Y == 1 {
		for _, s := range m.tabSpans {
			if msg.X >= s.x0 && msg.X < s.x1 {
				if m.menubar {
					m.expandTUI()
				}
				m.tab = s.tab
				m.expanded = false
				return m, nil
			}
		}
		if m.menubar {
			m.expandTUI()
		}
		return m, nil
	}
	// Agent tab row: switch the active agent. In the embedded layout the
	// terminal below follows and the bar collapses to menu + tabs only;
	// in the plain TUI the details card expands for the selection.
	if msg.Y == 2 {
		for i, s := range m.agentSpans {
			if msg.X >= s.x0 && msg.X < s.x1 {
				m.tab = tabAgents
				m.cursor = m.agentRows[i]
				if m.embedded {
					m.collapseToBar()
					if m.bottomPane != "" {
						return m, m.switchBottom(false)
					}
					return m, nil
				}
				m.expanded = true
				return m, nil
			}
		}
		return m, nil
	}
	if m.tab == tabSetup {
		mm := msg
		mm.Y = contentY
		return m.updateSetup(mm)
	}
	// Lifecycle buttons of the Agents view.
	if m.tab == tabAgents {
		for _, h := range m.actionHits {
			if msg.Y == h.y && msg.X >= h.x0 && msg.X < h.x1 {
				return m, m.lifecycle(h.action)
			}
		}
	}
	if m.menubar {
		// Bar mode: a click outside the two bars goes to the agent
		// terminal — the mouse sets the key focus there (typing reaches
		// the agent UI). Clicking the bar itself comes back to us.
		if msg.Y != 1 && msg.Y != 2 && m.bottomPane != "" {
			return m, m.focusBottom()
		}
		return m, nil
	}
	// Agent list rows: select; clicking the selected row toggles details.
	if m.tab == tabAgents && contentY >= m.listTop {
		row := contentY - m.listTop
		if row < len(m.data.agents) {
			if m.embedded {
				// Selecting an agent = agent mode: compact bar + its
				// terminal below.
				m.cursor = row
				m.expanded = false
				m.collapseToBar()
				if m.bottomPane != "" {
					return m, m.switchBottom(false)
				}
				return m, nil
			}
			if m.cursor == row {
				m.expanded = !m.expanded
			} else {
				m.cursor = row
				m.expanded = true
			}
		}
	}
	return m, nil
}

// openTerminal hands the agent terminal to tmux (split pane when running
// inside tmux, menu-bar-only above) or falls back to a full-screen attach.
// Failures are surfaced in the status line — never swallowed.
func (m *modelTUI) openTerminal() tea.Cmd {
	if len(m.data.agents) == 0 || m.cursor >= len(m.data.agents) {
		m.status = "no agent selected"
		return nil
	}
	a := m.data.agents[m.cursor]
	if a.State != model.AgentRunning {
		m.status = fmt.Sprintf("agent %s is %s — start it first", a.Name, a.State)
		return nil
	}
	// Embedded layout: the terminal pane below already hosts the agent —
	// just make sure it shows *this* agent and move focus into it.
	if m.embedded {
		if m.bottomPane == "" {
			m.status = "layout not ready yet"
			return nil
		}
		m.status = ""
		m.collapseToBar() // bar above the terminal
		return m.switchBottom(true)
	}
	if m.tmux.available() && m.tmux.inSession() {
		if err := m.tmux.splitAgent(a.Name, m.socket); err == nil {
			// The split shrinks our pane; render menu-bar-only.
			m.menubar = true
			m.status = ""
			return nil
		} else {
			m.status = "tmux split failed: " + err.Error() + " — falling back to full-screen"
		}
	}
	self, err := os.Executable()
	if err != nil {
		self = "dockweave"
	}
	c := exec.Command(self, "agent", "attach", "--term-socket", m.socket, a.Name)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return termDoneMsg{err: err}
	})
}

func (m *modelTUI) View() string {
	// Fixed geometry: line 0 = title, line 1 = menu bar (mouse targets),
	// line 2 = agent tabs, line 3+ = content. The content is windowed to
	// the pane height and scrolled — it never covers the menu.
	content := m.viewContent()
	var b strings.Builder
	b.WriteString(titleStyle.Render("DockWeave") + dimStyle.Render(" — dynamic workspaces for isolated AI agents") + "\n")

	// Menu bar with clickable tab spans (plain width = label + padding).
	m.tabSpans = m.tabSpans[:0]
	x := 0
	for i, name := range tabNames {
		w := len(name) + 2 // lipgloss Padding(0, 1)
		m.tabSpans = append(m.tabSpans, span{tab: tab(i), x0: x, x1: x + w})
		if tab(i) == m.tab {
			b.WriteString(tabActive.Render(name))
		} else {
			b.WriteString(tabIdle.Render(name))
		}
		x += w + 1
		if i < len(tabNames)-1 {
			b.WriteString(" ")
		}
	}
	if m.menubar {
		// Bar mode: menu + agent tabs only (tmux hosts the terminal below).
		b.WriteString("\n")
		m.renderAgentTabs(&b)
		b.WriteString("\n" + dimStyle.Render("tmux owns the terminal below • click menu: expand • click agent: switch terminal • click terminal: type • t: focus • q: quit"))
		return b.String()
	}
	b.WriteString("\n")

	// Agent tabs (running agents only): switch with a click.
	m.renderAgentTabs(&b)
	b.WriteString("\n")

	m.contentTop = 3
	b.WriteString(m.windowContent(content))
	m.writeStatus(&b)
	b.WriteString("\n" + dimStyle.Render("tab/click: switch • ↑/↓: select • enter/click: details • t: terminal • PgUp/PgDn: scroll • q: quit"))
	return b.String()
}

// windowContent fits the content into the remaining pane height and
// applies the scroll offset: the content must never cover the menu.
func (m *modelTUI) windowContent(content string) string {
	lines := strings.Split(content, "\n")
	used := m.contentTop + 1 // footer
	if m.status != "" {
		used++
	}
	avail := m.height - used
	if avail < 1 {
		avail = 1
	}
	maxScroll := len(lines) - avail
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}
	if m.scrollY < 0 {
		m.scrollY = 0
	}
	end := m.scrollY + avail
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[m.scrollY:end], "\n")
}

// scrollHint describes the scroll state when the content overflows.
func (m *modelTUI) scrollHint() string {
	if m.scrollY == 0 {
		return "PgUp/PgDn/scroll wheel: scroll"
	}
	return fmt.Sprintf("scroll %d+ • PgUp/PgDn/scroll wheel", m.scrollY)
}

// ensureRowVisible scrolls the content window so a row is visible.
func (m *modelTUI) ensureRowVisible(row int) {
	lines := m.height - m.contentTop - 2
	if lines < 1 {
		lines = 1
	}
	if row < m.scrollY {
		m.scrollY = row
	} else if row >= m.scrollY+lines {
		m.scrollY = row - lines + 1
	}
}

// isPrintableKey reports whether a key press inserts a character.
func isPrintableKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		return true
	}
	return len(msg.String()) == 1
}

// writeStatus renders the transient status line when set.
func (m *modelTUI) writeStatus(b *strings.Builder) {
	if m.status != "" {
		b.WriteString("\n" + errStyle.Render("! "+m.status))
	}
}

// viewContent renders the active tab's body (without the global chrome).
func (m *modelTUI) viewContent() string {
	var b strings.Builder
	if m.data.err != nil {
		b.WriteString(errStyle.Render("daemon: "+m.data.err.Error()) + "\n")
		b.WriteString(dimStyle.Render("is dockweaved running? socket reachable?") + "\n")
		return b.String()
	}
	switch m.tab {
	case tabDashboard:
		b.WriteString(m.viewDashboard())
	case tabAgents:
		b.WriteString(m.viewAgents())
	case tabWorkspaces:
		b.WriteString(m.viewWorkspaces())
	case tabMounts:
		b.WriteString(m.viewMounts())
	case tabEvents:
		b.WriteString(m.viewEvents())
	case tabSetup:
		b.WriteString(m.viewSetup())
	}
	return b.String()
}

func (m *modelTUI) viewDashboard() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %-20s %-8s %-10s %s\n", "AGENT", "PROJECT", "ACCESS", "ACTIVITY", "CPU/MEM")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 72))
	actByAgent := map[string]*model.AgentActivity{}
	for _, a := range m.data.activity {
		actByAgent[a.AgentID] = a
	}
	// Map agent -> workspaces from mounts.
	wsByAgent := map[string][]*model.MountState{}
	for _, mt := range m.data.mounts {
		if mt.Status == model.MountActive {
			wsByAgent[mt.AgentID] = append(wsByAgent[mt.AgentID], mt)
		}
	}
	active := 0
	for _, a := range m.data.agents {
		act := actByAgent[a.ID]
		mounts := wsByAgent[a.ID]
		proj, access := "-", "-"
		if len(mounts) > 0 {
			proj = lastElem(mounts[0].TargetPath)
			access = strings.ToUpper(string(mounts[0].Mode))
		}
		activity := dimStyle.Render("IDLE")
		cpu, mem := "-", "-"
		if act != nil {
			if act.State == "running" {
				active++
			}
			switch act.Activity {
			case model.ActivityWriting:
				activity = warnStyle.Render("WRITING")
			case model.ActivityReading:
				activity = okStyle.Render("READING")
			case model.ActivityUnknown:
				activity = dimStyle.Render("UNKNOWN")
			}
			if act.Available {
				cpu = fmt.Sprintf("%.0f%%", act.CPUPercent)
				mem = fmt.Sprintf("%dMiB", act.MemoryBytes/1024/1024)
			}
		}
		fmt.Fprintf(&b, "%-16s %-20s %-8s %-21s %s/%s\n", a.Name, proj, access, activity, cpu, mem)
	}
	fmt.Fprintf(&b, "\n%d Agents | %d Workspaces | %d Active\n",
		len(m.data.agents), len(m.data.workspaces), active)
	if len(m.data.conflicts) > 0 {
		fmt.Fprintf(&b, "\n%s\n", errStyle.Render(fmt.Sprintf("⚠ %d file conflict(s):", len(m.data.conflicts))))
		for _, c := range m.data.conflicts {
			fmt.Fprintf(&b, "  %s  agents=%v writers=%v\n", c.Path, c.AgentIDs, c.Writers)
		}
	}
	return b.String()
}

func (m *modelTUI) viewAgents() string {
	var b strings.Builder
	m.listTop = 2 // content rows: header(0) + divider(1) + rows from 2
	fmt.Fprintf(&b, "%-16s %-10s %-30s %s\n", "NAME", "STATE", "IMAGE", "MEM")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 78))
	for i, a := range m.data.agents {
		st := string(a.State)
		if a.State == model.AgentRunning {
			st = okStyle.Render(st)
		}
		marker := "  "
		if i == m.cursor && m.tab == tabAgents {
			marker = tabActive.Render("▸ ")
		}
		fmt.Fprintf(&b, "%s%-14s %-19s %-30s %s\n", marker, padEnd(a.Name, 14), st, a.Image, shortID(m.memoryOf(a.ID)))
	}
	if m.expanded && m.cursor < len(m.data.agents) {
		b.WriteString("\n" + m.viewAgentDetail(m.data.agents[m.cursor]))
	}
	if m.cursor < len(m.data.agents) {
		b.WriteString(m.viewAgentActions(m.data.agents[m.cursor]))
	} else if !m.expanded {
		b.WriteString("\n" + dimStyle.Render("t: terminal • enter/click: details"))
	}
	return b.String()
}

// viewAgentActions renders the lifecycle actions of the selected agent:
// state-aware (the meaningful action is emphasised), clickable and bound
// to s/x keys.
func (m *modelTUI) viewAgentActions(a *model.Agent) string {
	running := a.State == model.AgentRunning
	startEm, stopEm := !running, running
	m.actionHits = m.actionHits[:0]
	var b strings.Builder
	b.WriteString("\n  ")
	m.writeActionButton(&b, "s start", "start", startEm)
	b.WriteString("  ")
	m.writeActionButton(&b, "x stop", "stop", stopEm)
	b.WriteString("  ")
	m.writeActionButton(&b, "restart", "restart", false)
	b.WriteString("  ")
	m.writeActionButton(&b, "remove", "remove", false)
	state := "stopped"
	if running {
		state = "running"
	}
	b.WriteString("   " + dimStyle.Render("state: "+state))
	b.WriteString("\n" + dimStyle.Render("keys: s start • x stop • t: terminal • enter: details"))
	return b.String()
}

// writeActionButton renders one lifecycle button and records its click
// region (x of the label start, on the current last line).
func (m *modelTUI) writeActionButton(b *strings.Builder, label, action string, emph bool) {
	start := len(b.String())
	if emph {
		b.WriteString(tabActive.Render("▸" + label + " "))
	} else {
		b.WriteString(dimStyle.Render("[" + label + "]"))
	}
	m.actionHits = append(m.actionHits, setupHit{y: strings.Count(b.String(), "\n"), x0: start, x1: len(b.String()), action: action})
}

// renderAgentTabs draws the running-agent tab row (line 2, clickable).
// Labels are "agent:workspace" so every tab is unambiguous.
func (m *modelTUI) renderAgentTabs(b *strings.Builder) {
	m.agentSpans = m.agentSpans[:0]
	m.agentRows = m.agentRows[:0]
	x := 0
	any := false
	for i, a := range m.data.agents {
		if a.State != model.AgentRunning {
			continue
		}
		any = true
		label := m.agentTabLabel(a)
		w := len(label) + 2 // padding(0,1)
		m.agentSpans = append(m.agentSpans, span{tab: tabAgents, x0: x, x1: x + w})
		m.agentRows = append(m.agentRows, i)
		active := i == m.cursor
		if active {
			b.WriteString(tabActive.Render(label))
		} else {
			b.WriteString(tabIdle.Render(label))
		}
		x += w + 1
		b.WriteString(" ")
	}
	if !any {
		b.WriteString(dimStyle.Render("no running agents"))
	}
}

// agentTabLabel returns the unique tab label "agent:workspace" for a
// running agent (the workspace is its primary active share).
func (m *modelTUI) agentTabLabel(a *model.Agent) string {
	ws := ""
	for _, mt := range m.data.mounts {
		if mt.AgentID == a.ID && mt.Status == model.MountActive {
			ws = lastElem(mt.TargetPath)
			break
		}
	}
	if ws == "" {
		return a.Name
	}
	return a.Name + ":" + ws
}

// shortID abbreviates registry IDs for compact display.
func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

// memoryOf returns the memory identity currently bound to an agent.
func (m *modelTUI) memoryOf(agentID string) string {
	for _, mem := range m.data.memories {
		if mem.OwnerAgentID == agentID {
			return mem.ID
		}
	}
	return ""
}

// viewAgentDetail is the expandable card shown only for the selected agent
// (compact default; details on selection).
func (m *modelTUI) viewAgentDetail(a *model.Agent) string {
	var b strings.Builder
	b.WriteString(tabActive.Render(" details ") + "\n")
	fmt.Fprintf(&b, "  id        %s\n", a.ID)
	fmt.Fprintf(&b, "  type      %s\n", a.Type)
	fmt.Fprintf(&b, "  image     %s\n", a.Image)
	if a.UID != 0 || a.GID != 0 {
		fmt.Fprintf(&b, "  identity  %d:%d\n", a.UID, a.GID)
	}
	b.WriteString(m.viewAgentFolders(a))
	for _, act := range m.data.activity {
		if act.AgentID != a.ID {
			continue
		}
		fmt.Fprintf(&b, "  activity  %s", act.Activity)
		if act.Available {
			fmt.Fprintf(&b, "  cpu %.0f%%  mem %dMiB", act.CPUPercent, act.MemoryBytes/1024/1024)
		}
		b.WriteString("\n")
		for p, ev := range act.FileEvidence {
			fmt.Fprintf(&b, "    file %s (%s)\n", p, ev)
		}
	}
	return b.String()
}

// viewAgentFolders lists the folder assignments of an agent container:
// which host directory backs /agent (settings, AGENTS.md, skills),
// /memory (long-term memory) and /workspace (the project mounts).
func (m *modelTUI) viewAgentFolders(a *model.Agent) string {
	var b strings.Builder
	p, ok := m.data.paths[a.ID]
	b.WriteString(tabActive.Render(" folders ") + "\n")
	if ok {
		fmt.Fprintf(&b, "  agent     /agent ← %s   (AGENTS.md, skills)\n", p.AgentDir)
		if p.MemoryDir != "" {
			fmt.Fprintf(&b, "  memory    /memory ← %s   (long-term memory)\n", p.MemoryDir)
		} else {
			b.WriteString("  memory    /memory ← (not attached)\n")
		}
	}
	mounted := false
	for _, mt := range m.data.mounts {
		if mt.AgentID == a.ID && mt.Status == model.MountActive {
			fmt.Fprintf(&b, "  project   /workspace/%s ← %s   (%s)\n", lastElem(mt.TargetPath), mt.HostPath, mt.Mode)
			mounted = true
		}
	}
	if !mounted {
		b.WriteString("  project   /workspace ← (no project assigned)\n")
	}
	return b.String()
}

func padEnd(s string, n int) string {
	// lipgloss styles break padding; keep plain names for alignment.
	if len(s) > n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

func (m *modelTUI) viewWorkspaces() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-50s %s\n", "NAME", "PATH", "ID")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 84))
	for _, w := range m.data.workspaces {
		fmt.Fprintf(&b, "%-20s %-50s %s\n", w.Name, w.HostPath, w.ID)
	}
	return b.String()
}

func (m *modelTUI) viewMounts() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s %-40s %-5s %-9s %s\n", "AGENT", "TARGET", "MODE", "STATUS", "SOURCE")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 90))
	for _, mt := range m.data.mounts {
		st := string(mt.Status)
		if mt.Status == model.MountActive {
			st = okStyle.Render(st)
		} else if mt.Status == model.MountFailed {
			st = errStyle.Render(st)
		}
		fmt.Fprintf(&b, "%-12s %-40s %-5s %-18s %s\n", mt.AgentID, mt.TargetPath, mt.Mode, st, mt.HostPath)
		if mt.LastError != "" {
			fmt.Fprintf(&b, "  %s\n", errStyle.Render("error: "+mt.LastError))
		}
	}
	return b.String()
}

func (m *modelTUI) viewEvents() string {
	var b strings.Builder
	for i := len(m.data.events) - 1; i >= 0; i-- {
		e := m.data.events[i]
		line := fmt.Sprintf("%s  %-18s %-10s %s", e.Timestamp.Format("15:04:05"), e.Type, e.Actor, e.Message)
		if e.Type == model.EventMountFailed || e.Type == model.EventSecurityDeny {
			line = errStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func lastElem(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
