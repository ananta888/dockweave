package tui

// setup.go: the "Setup" tab — create agents and manage folder assignments
// with mouse and keyboard. Modern form behaviour: text inputs (bubbles),
// focus cycling (tab/↑↓), buttons, one-click assign/unassign of project
// folders. All actions go through the daemon API and refresh the view.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
)

var agentTypes = []string{"codex", "opencode", "claude", "gemini", "custom"}

// agentPreset holds the default image and command per agent profile. The
// packages are the verified npm distributions; installs go to $HOME/.local
// so they persist in /memory.
type agentPreset struct {
	image   string
	command string
}

var agentPresets = map[string]agentPreset{
	"codex": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @openai/codex && exec "$HOME/.local/bin/codex"`,
	},
	"opencode": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" opencode-ai && exec "$HOME/.local/bin/opencode"`,
	},
	"claude": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @anthropic-ai/claude-code && exec "$HOME/.local/bin/claude"`,
	},
	"gemini": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @google/gemini-cli && exec "$HOME/.local/bin/gemini"`,
	},
	"custom": {},
}

// setupHit is a clickable region of the Setup view (plain columns/rows).
type setupHit struct {
	y, x0, x1 int
	action    string // "f-name" .. field focus, "type", "create", "clear", "ws:<id>", "f-wsname", ...
	arg       string
}

// setupForm holds the Setup tab state.
type setupForm struct {
	name, image, cmd, uid textinput.Model
	wsName, wsPath        textinput.Model
	agentType             int    // index into agentTypes
	focus                 string // current hit action, "" = none
	hits                  []setupHit
	// touched marks fields the user typed into: type changes never
	// overwrite them. seeded applies the defaults once on first entry.
	touched map[string]bool
	seeded  bool
}

func newSetupForm() setupForm {
	mk := func(prompt, placeholder string, limit int) textinput.Model {
		ti := textinput.New()
		ti.Prompt = prompt
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		return ti
	}
	return setupForm{
		name:    mk("Name    ", "e.g. codex-02", 40),
		image:   mk("Image   ", "node:22-bookworm", 80),
		cmd:     mk("Command ", `sh -c '…'`+" or empty for the image default", 160),
		uid:     mk("UID     ", "1000", 8),
		wsName:  mk("Name    ", "myproject", 40),
		wsPath:  mk("Path    ", "/srv/projects/myproject", 120),
		touched: map[string]bool{},
	}
}

// applyTypeDefaults pre-fills name, image and command for the selected
// agent profile. Fields the user typed into are never overwritten.
func (m *modelTUI) applyTypeDefaults() {
	f := &m.form
	t := agentTypes[f.agentType]
	p := agentPresets[t]
	if !f.touched["name"] {
		f.name.SetValue(m.nextAgentName(t))
	}
	if !f.touched["image"] && p.image != "" {
		f.image.SetValue(p.image)
	}
	if !f.touched["cmd"] && p.command != "" {
		f.cmd.SetValue(p.command)
	}
	if !f.touched["uid"] {
		f.uid.SetValue("1000")
	}
	f.seeded = true
}

// nextAgentName suggests "<type>-NN" with the next free number.
func (m *modelTUI) nextAgentName(t string) string {
	used := map[string]bool{}
	for _, a := range m.data.agents {
		used[a.Name] = true
	}
	for n := 1; n < 100; n++ {
		cand := fmt.Sprintf("%s-%02d", t, n)
		if !used[cand] {
			return cand
		}
	}
	return t
}

// clearCreateForm resets the create form to the profile defaults.
func (m *modelTUI) clearCreateForm() {
	f := &m.form
	for _, k := range []string{"name", "image", "cmd", "uid"} {
		delete(f.touched, k)
	}
	f.name.SetValue("")
	f.image.SetValue("")
	f.cmd.SetValue("")
	f.uid.SetValue("")
	m.applyTypeDefaults()
}

// focusables lists the focusable actions in tab order.
func (f *setupForm) focusables() []string {
	return []string{
		"f-name", "f-image", "f-cmd", "f-uid", "type",
		"create", "clear",
		"f-wsname", "f-wspath", "register",
	}
}

func (f *setupForm) applyFocus(action string) {
	for _, ti := range []*textinput.Model{&f.name, &f.image, &f.cmd, &f.uid, &f.wsName, &f.wsPath} {
		ti.Blur()
	}
	f.focus = action
	switch action {
	case "f-name":
		f.name.Focus()
	case "f-image":
		f.image.Focus()
	case "f-cmd":
		f.cmd.Focus()
	case "f-uid":
		f.uid.Focus()
	case "f-wsname":
		f.wsName.Focus()
	case "f-wspath":
		f.wsPath.Focus()
	}
}

func (f *setupForm) cycleFocus(dir int) {
	list := f.focusables()
	idx := 0
	for i, a := range list {
		if a == f.focus {
			idx = i
		}
	}
	idx = (idx + dir + len(list)) % len(list)
	f.applyFocus(list[idx])
}

func (f *setupForm) focusedInput() *textinput.Model {
	switch f.focus {
	case "f-name":
		return &f.name
	case "f-image":
		return &f.image
	case "f-cmd":
		return &f.cmd
	case "f-uid":
		return &f.uid
	case "f-wsname":
		return &f.wsName
	case "f-wspath":
		return &f.wsPath
	}
	return nil
}

// setupDoneMsg reports the outcome of a Setup action.
type setupDoneMsg struct {
	err error
}

// selectedAgent returns the agent the folder section refers to.
func (m *modelTUI) selectedAgent() *model.Agent {
	if m.cursor < len(m.data.agents) {
		return m.data.agents[m.cursor]
	}
	return nil
}

// grantFor returns the active grant of the selected agent on a workspace.
func (m *modelTUI) grantFor(agentID, workspaceID string) *model.Grant {
	for i := range m.data.grants {
		g := m.data.grants[i]
		if g.AgentID == agentID && g.WorkspaceID == workspaceID && g.Active(time.Now()) {
			return g
		}
	}
	return nil
}

// ---- view ----

func (m *modelTUI) viewSetup() string {
	var b strings.Builder
	f := &m.form
	if !f.seeded {
		m.applyTypeDefaults()
	}
	f.hits = f.hits[:0]
	row := m.setupRowBase()

	hit := func(y, x0, x1 int, action, arg string) {
		f.hits = append(f.hits, setupHit{y: y, x0: x0, x1: x1, action: action, arg: arg})
	}

	// -- create agent --
	b.WriteString(tabActive.Render(" create agent ") + "\n")
	row++
	b.WriteString("  " + f.name.View() + "   Type " + typeCyclerView(f) + "\n")
	hit(row, 2, 2+40, "f-name", "")
	hit(row, 52, 72, "type", "")
	row++
	b.WriteString("  " + f.image.View() + "   " + f.uid.View() + "\n")
	hit(row, 2, 2+50, "f-image", "")
	hit(row, 52, 52+12, "f-uid", "")
	row++
	b.WriteString("  " + f.cmd.View() + "\n")
	hit(row, 2, 2+80, "f-cmd", "")
	row++
	b.WriteString("  " + buttonView(" Create ", f.focus == "create") + "  " + buttonView(" Clear ", f.focus == "clear") + "\n")
	hit(row, 2, 12, "create", "")
	hit(row, 15, 24, "clear", "")
	row++

	// -- folders of the selected agent --
	agentName := "(no agent selected)"
	var a *model.Agent
	if a = m.selectedAgent(); a != nil {
		agentName = a.Name
	}
	b.WriteString("\n" + tabActive.Render(" folders · "+agentName+" ") + "\n")
	row += 2
	if a != nil {
		if p, ok := m.data.paths[a.ID]; ok {
			fmt.Fprintf(&b, "  %-8s /agent  ← %s   %s\n", "agent", p.AgentDir, dimStyle.Render("(AGENTS.md, skills)"))
			row++
			mem := p.MemoryDir
			if mem == "" {
				mem = "(not attached)"
			}
			fmt.Fprintf(&b, "  %-8s /memory ← %s   %s\n", "memory", mem, dimStyle.Render("(long-term memory)"))
			row++
		}
		assigned := map[string]*model.Grant{}
		for _, w := range m.data.workspaces {
			if g := m.grantFor(a.ID, w.ID); g != nil {
				assigned[w.ID] = g
			}
		}
		for _, w := range m.data.workspaces {
			mark := "  "
			action, label := "assign", buttonView(" assign ", false)
			if g := assigned[w.ID]; g != nil {
				mark = okStyle.Render("● ")
				action = "unassign"
				label = buttonView(" unassign ", f.focus == "unassign:"+w.ID)
			}
			fmt.Fprintf(&b, "  %s%-8s /workspace/%s ← %s   %s\n", mark, "project", w.Name, w.HostPath, label)
			hit(row, 60, 76, action+":"+w.ID, w.ID)
			row++
		}
		if len(m.data.workspaces) == 0 {
			b.WriteString("  " + dimStyle.Render("(no workspaces registered yet)") + "\n")
			row++
		}
	}

	// -- register workspace --
	b.WriteString("\n" + tabActive.Render(" register workspace ") + "\n")
	row += 2
	b.WriteString("  " + f.wsName.View() + "  " + f.wsPath.View() + "\n")
	hit(row, 2, 2+50, "f-wsname", "")
	hit(row, 52, 52+70, "f-wspath", "")
	row++
	b.WriteString("  " + buttonView(" Register ", f.focus == "register") + "\n")
	hit(row, 2, 14, "register", "")

	return b.String()
}

// setupRowBase is the first content row of the view (fixed geometry for
// mouse hit-testing): title(0), menu(1), agent tabs(2), blank(3).
func (m *modelTUI) setupRowBase() int { return 3 }

func typeCyclerView(f *setupForm) string {
	label := "‹ " + agentTypes[f.agentType] + " ›"
	if f.focus == "type" {
		return tabActive.Render(label)
	}
	return tabIdle.Render(label)
}

func buttonView(label string, focused bool) string {
	if focused {
		return tabActive.Render(label)
	}
	return dimStyle.Render("[" + strings.TrimSpace(label) + "]")
}

// ---- update ----

// updateSetup handles keyboard and mouse for the Setup tab.
func (m *modelTUI) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.form
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			f.cycleFocus(1)
			return m, nil
		case "shift+tab":
			f.cycleFocus(-1)
			return m, nil
		case "up":
			f.cycleFocus(-1)
			return m, nil
		case "down", "enter":
			if msg.String() == "enter" {
				if cmd := m.activate(f.focus, f.focus); cmd != nil {
					return m, cmd
				}
			}
			f.cycleFocus(1)
			return m, nil
		case "left", "right":
			if f.focus == "type" {
				m.cycleType(msg.String() == "right")
			}
			return m, nil
		}
		if ti := f.focusedInput(); ti != nil {
			var cmd tea.Cmd
			*ti, cmd = ti.Update(msg)
			// User input protects the field from default overwrites.
			switch msg.String() {
			case "backspace", "delete", "ctrl+u", "ctrl+k":
				f.touched[strings.TrimPrefix(f.focus, "f-")] = true
			default:
				if len(msg.String()) == 1 || msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
					f.touched[strings.TrimPrefix(f.focus, "f-")] = true
				}
			}
			return m, cmd
		}
		return m, nil
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		y := msg.Y
		for _, h := range f.hits {
			if h.y == y && msg.X >= h.x0 && msg.X < h.x1 {
				return m, m.activate(h.action, h.arg)
			}
		}
	}
	return m, nil
}

// cycleType cycles the agent type cycler and refreshes the defaults.
func (m *modelTUI) cycleType(next bool) {
	if next {
		m.form.agentType = (m.form.agentType + 1) % len(agentTypes)
	} else {
		m.form.agentType = (m.form.agentType - 1 + len(agentTypes)) % len(agentTypes)
	}
	m.applyTypeDefaults()
}

// activate performs the action of a focused/clicked control.
func (m *modelTUI) activate(action, arg string) tea.Cmd {
	f := &m.form
	switch {
	case action == "type":
		m.cycleType(true)
		f.applyFocus("type")
		return nil
	case action == "create":
		return m.submitAgent()
	case action == "clear":
		m.clearCreateForm()
		m.status = ""
		return nil
	case action == "register":
		return m.submitWorkspace()
	case strings.HasPrefix(action, "assign:"):
		return m.submitAssign(strings.TrimPrefix(action, "assign:"), "rw")
	case strings.HasPrefix(action, "unassign:"):
		return m.submitUnassign(strings.TrimPrefix(action, "unassign:"))
	case strings.HasPrefix(action, "f-"):
		f.applyFocus(action)
		return nil
	}
	return nil
}

// submitAgent creates the agent from the form values.
func (m *modelTUI) submitAgent() tea.Cmd {
	f := &m.form
	name := strings.TrimSpace(f.name.Value())
	image := strings.TrimSpace(f.image.Value())
	if name == "" || image == "" {
		m.status = "create agent: name and image are required"
		return nil
	}
	spec := sdk.AgentSpec{
		Name: name, Type: agentTypes[f.agentType], Image: image,
	}
	if c := strings.TrimSpace(f.cmd.Value()); c != "" {
		// Quoted form: one shell string wrapped in sh -c.
		spec.Command = []string{"sh", "-c", strings.TrimPrefix(strings.TrimPrefix(c, "sh,"), "-c,")}
	}
	if u := strings.TrimSpace(f.uid.Value()); u != "" {
		if n, err := strconv.Atoi(u); err == nil {
			spec.UID, spec.GID = n, n
		}
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := client.CreateAgent(ctx, spec)
		return setupDoneMsg{err: err}
	}
}

// submitWorkspace registers a new workspace directory.
func (m *modelTUI) submitWorkspace() tea.Cmd {
	f := &m.form
	name := strings.TrimSpace(f.wsName.Value())
	path := strings.TrimSpace(f.wsPath.Value())
	if name == "" || path == "" {
		m.status = "register workspace: name and path are required"
		return nil
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.RegisterWorkspace(ctx, name, path)
		return setupDoneMsg{err: err}
	}
}

// submitAssign grants the selected agent access to a workspace.
func (m *modelTUI) submitAssign(workspaceID, mode string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	var ws *model.Workspace
	for _, w := range m.data.workspaces {
		if w.ID == workspaceID {
			ws = w
		}
	}
	if ws == nil {
		return nil
	}
	client, agent, name := m.client, a.Name, ws.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.Grant(ctx, name, agent, mode, false, "")
		return setupDoneMsg{err: err}
	}
}

// submitUnassign revokes the grant binding a workspace to the agent.
func (m *modelTUI) submitUnassign(workspaceID string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	g := m.grantFor(a.ID, workspaceID)
	if g == nil {
		return nil
	}
	client, id := m.client, g.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.RevokeGrant(ctx, id, false)
		return setupDoneMsg{err: err}
	}
}
