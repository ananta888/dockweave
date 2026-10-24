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
	tabCount
)

var tabNames = []string{"Dashboard", "Agents", "Workspaces", "Mounts", "Events"}

type dataMsg struct {
	agents     []*model.Agent
	activity   []*model.AgentActivity
	workspaces []*model.Workspace
	mounts     []*model.MountState
	events     []*model.Event
	conflicts  []model.FileConflict
	memories   []sdk.MemoryInfo
	err        error
}

type tickMsg time.Time

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
	// menubar mode: only the menu bar is rendered (tmux split hosts the
	// agent terminal below; see tmux.go).
	menubar bool
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

// Run starts the TUI against the daemon at socketPath.
func Run(socketPath string) error {
	termSocket := filepath.Join(filepath.Dir(socketPath), "dockweaved.term.sock")
	m := &modelTUI{client: sdk.New(socketPath), keys: defaultKeys(), socket: termSocket, tmux: &realTmux{}}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *modelTUI) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick())
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
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Next):
			m.tab = (m.tab + 1) % tabCount
			m.expanded = false
		case key.Matches(msg, m.keys.Prev):
			m.tab = (m.tab - 1 + tabCount) % tabCount
			m.expanded = false
		case key.Matches(msg, m.keys.Refresh):
			return m, m.refresh()
		case key.Matches(msg, m.keys.CursorUp) && m.tab == tabAgents && !m.menubar:
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.CursorDown) && m.tab == tabAgents && !m.menubar:
			if m.cursor < len(m.data.agents)-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.Term) && m.tab == tabAgents && !m.menubar:
			return m, m.openTerminal()
		case msg.String() == "enter" && m.tab == tabAgents && !m.menubar:
			// Toggle the detail card (compact by default).
			m.expanded = !m.expanded
		case msg.String() == "esc":
			if m.menubar {
				// Leave menu-bar-only mode: give the TUI its height back.
				m.menubar = false
				m.tmux.resizeSelf(m.height + 20)
				return m, nil
			}
			m.expanded = false
		}
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case dataMsg:
		m.data = msg
	}
	return m, nil
}

// handleMouse maps left-button clicks on the menu bar and the agent list.
func (m *modelTUI) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	// Menu bar: switch tabs.
	if msg.Y == 1 {
		for _, s := range m.tabSpans {
			if msg.X >= s.x0 && msg.X < s.x1 {
				m.tab = s.tab
				m.expanded = false
				return m, nil
			}
		}
		return m, nil
	}
	// Agent tab row: switch the active agent (select + expand details).
	if msg.Y == 2 {
		for i, s := range m.agentSpans {
			if msg.X >= s.x0 && msg.X < s.x1 {
				m.tab = tabAgents
				m.cursor = m.agentRows[i]
				m.expanded = true
				return m, nil
			}
		}
		return m, nil
	}
	if m.menubar {
		return m, nil
	}
	// Agent list rows: select; clicking the selected row toggles details.
	if m.tab == tabAgents && msg.Y >= m.listTop {
		row := msg.Y - m.listTop
		if row < len(m.data.agents) {
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
func (m *modelTUI) openTerminal() tea.Cmd {
	if len(m.data.agents) == 0 || m.cursor >= len(m.data.agents) {
		return nil
	}
	a := m.data.agents[m.cursor]
	if m.tmux.available() && m.tmux.inSession() {
		if err := m.tmux.splitAgent(a.Name, m.socket); err == nil {
			// The split shrinks our pane; render menu-bar-only.
			m.menubar = true
			return nil
		}
		// fall through to the exec-based attach
	}
	self, err := os.Executable()
	if err != nil {
		self = "dockweave"
	}
	c := exec.Command(self, "agent", "attach", "--term-socket", m.socket, a.Name)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return dataMsg{err: nil} // triggers redraw after return
	})
}

func (m *modelTUI) View() string {
	var b strings.Builder
	// Fixed geometry: line 0 = title, line 1 = menu bar (mouse targets),
	// line 2+ = content. This keeps click hit-testing stable.
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
		b.WriteString("\n" + dimStyle.Render("tmux owns the terminal below • esc: expand TUI • q: quit"))
		return b.String()
	}
	b.WriteString("\n")

	// Agent tabs (running agents only): switch with a click.
	m.renderAgentTabs(&b)
	b.WriteString("\n")

	if m.data.err != nil {
		b.WriteString(errStyle.Render("daemon: "+m.data.err.Error()) + "\n")
		b.WriteString(dimStyle.Render("is dockweaved running? socket reachable?") + "\n")
	} else {
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
		}
	}
	b.WriteString("\n" + dimStyle.Render("tab/click: switch • ↑/↓: select • enter/click: details • t: terminal • r: refresh • q: quit"))
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
	m.listTop = 5 // title(0) + tabs(1) + agent tabs(2) + header(3) + divider(4)
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
	} else {
		b.WriteString("\n" + dimStyle.Render("t: terminal • enter/click: details"))
	}
	return b.String()
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
	if mem := m.memoryOf(a.ID); mem != "" {
		fmt.Fprintf(&b, "  memory    %s\n", mem)
	}
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
	for _, mt := range m.data.mounts {
		if mt.AgentID == a.ID {
			fmt.Fprintf(&b, "  mount     %s → %s (%s)\n", mt.HostPath, lastElem(mt.TargetPath), mt.Mode)
		}
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
