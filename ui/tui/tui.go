// Package tui implements the DockWeave terminal UI (Bubble Tea).
// All displayed data comes from the daemon API — never fabricated.
package tui

import (
	"context"
	"fmt"
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
	agents    []*model.Agent
	activity  []*model.AgentActivity
	workspaces []*model.Workspace
	mounts    []*model.MountState
	events    []*model.Event
	conflicts []model.FileConflict
	err       error
}

type tickMsg time.Time

type modelTUI struct {
	client *sdk.Client
	tab    tab
	data   dataMsg
	width  int
	height int
	keys   keyMap
}

type keyMap struct {
	Quit, Next, Prev, Refresh key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Next:    key.NewBinding(key.WithKeys("tab", "l"), key.WithHelp("tab", "next view")),
		Prev:    key.NewBinding(key.WithKeys("shift+tab", "h"), key.WithHelp("shift+tab", "prev view")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	}
}

// Run starts the TUI against the daemon at socketPath.
func Run(socketPath string) error {
	m := &modelTUI{client: sdk.New(socketPath), keys: defaultKeys()}
	p := tea.NewProgram(m, tea.WithAltScreen())
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
		d.err = nil
		return d
	}
}

func (m *modelTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Next):
			m.tab = (m.tab + 1) % tabCount
		case key.Matches(msg, m.keys.Prev):
			m.tab = (m.tab - 1 + tabCount) % tabCount
		case key.Matches(msg, m.keys.Refresh):
			return m, m.refresh()
		}
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case dataMsg:
		m.data = msg
	}
	return m, nil
}

func (m *modelTUI) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("DockWeave") + dimStyle.Render(" — dynamic workspaces for isolated AI agents") + "\n\n")
	// Tabs
	for i, name := range tabNames {
		if tab(i) == m.tab {
			b.WriteString(tabActive.Render(name))
		} else {
			b.WriteString(tabIdle.Render(name))
		}
	}
	b.WriteString("\n\n")
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
	b.WriteString("\n" + dimStyle.Render("tab: next • shift+tab: prev • r: refresh • q: quit"))
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
	fmt.Fprintf(&b, "%-16s %-10s %-10s %-30s %s\n", "NAME", "TYPE", "STATE", "IMAGE", "ID")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 80))
	for _, a := range m.data.agents {
		st := string(a.State)
		if a.State == model.AgentRunning {
			st = okStyle.Render(st)
		}
		fmt.Fprintf(&b, "%-16s %-10s %-19s %-30s %s\n", a.Name, a.Type, st, a.Image, a.ID)
	}
	return b.String()
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
