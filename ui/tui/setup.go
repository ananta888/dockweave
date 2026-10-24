package tui

// setup.go: the "Setup" tab — create agents and manage folder assignments
// (/agent, /memory, /workspace) with mouse and keyboard. Every clickable
// element is also a keyboard focus target (tab/↑↓ cycle, enter activates);
// text fields accept typing. Profile defaults pre-fill the create form.

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

var agentTypes = []string{"codex", "opencode", "claude", "antigravity", "gemini", "custom"}

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
	"antigravity": {
		// Google's current coding agent (gemini-cli is legacy). Official
		// installer from antigravity.google/docs/getting-started; the
		// binary lands in $HOME/.local/bin/agy (= /memory, persistent).
		image:   "node:22-bookworm",
		command: `curl -fsSL https://antigravity.google/cli/install.sh | bash && exec "$HOME/.local/bin/agy"`,
	},
	"gemini": {
		// Legacy (Google: "Migrating from Gemini CLI").
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @google/gemini-cli && exec "$HOME/.local/bin/gemini"`,
	},
	"custom": {},
}

// setupHit is one interactive control of the Setup view. The hit list is
// both the mouse hit map and the keyboard focus order.
type setupHit struct {
	y, x0, x1 int
	action    string
	arg       string
}

// setupForm holds the Setup tab state.
type setupForm struct {
	name, image, cmd, uid textinput.Model
	folder                textinput.Model
	wsName, wsPath        textinput.Model
	agentType             int  // index into agentTypes
	folderTarget          int  // 0 = /agent folder, 1 = new memory folder
	mode                  int  // 0 = agent settings, 1 = new agent, 2 = workspaces
	showStores            bool // memory store list expanded
	focusIdx              int  // index into hits, -1 = nothing focused
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
		name:     mk("Name    ", "e.g. codex-02", 40),
		image:    mk("Image   ", "node:22-bookworm", 80),
		cmd:      mk("Command ", "npm install -g --prefix ... && exec ...", 160),
		uid:      mk("UID     ", "1000", 8),
		folder:   mk("folder  ", "/home/me/my-agent-settings", 120),
		wsName:   mk("Name    ", "myproject", 40),
		wsPath:   mk("Path    ", "/srv/projects/myproject", 120),
		focusIdx: -1,
		touched:  map[string]bool{},
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

// setupDoneMsg reports the outcome of a Setup action.
type setupDoneMsg struct {
	err error
}

// ---- view ----

func (m *modelTUI) viewSetup() string {
	var b strings.Builder
	f := &m.form
	if !f.seeded {
		m.applyTypeDefaults()
	}
	f.hits = f.hits[:0]
	row := 3
	hit := func(y, x0, x1 int, action, arg string) {
		f.hits = append(f.hits, setupHit{y: y, x0: x0, x1: x1, action: action, arg: arg})
	}
	focused := func(action string) bool {
		return f.focusIdx >= 0 && f.focusIdx < len(f.hits) && f.hits[f.focusIdx].action == action
	}

	// -- agent tab area: click an agent to see and change its settings --
	x := 2
	chip := func(label string, y int, action, arg string, active bool) {
		text := " " + label + " "
		w := len(text) + 2
		if active {
			b.WriteString(tabActive.Render(text))
		} else {
			b.WriteString(tabIdle.Render(text))
		}
		hit(y, x, x+w, action, arg)
		x += w + 1
	}
	b.WriteString(" ")
	for i, a := range m.data.agents {
		chip(a.Name, row, "select:"+a.ID, a.ID, f.mode == 0 && m.cursor == i)
	}
	chip("+ new agent", row, "mode:1", "", f.mode == 1)
	chip("workspaces", row, "mode:2", "", f.mode == 2)
	b.WriteString("\n")
	row++

	switch f.mode {
	case 1:
		return b.String() + m.viewSetupNew(&row, hit, focused)
	case 2:
		return b.String() + m.viewSetupWorkspaces(&row, hit, focused)
	}
	return b.String() + m.viewSetupSettings(&row, hit, focused)
}

// viewSetupSettings shows the configuration of the selected agent.
func (m *modelTUI) viewSetupSettings(row *int, hit func(int, int, int, string, string), focused func(string) bool) string {
	var b strings.Builder
	f := &m.form
	a := m.selectedAgent()
	if a == nil {
		b.WriteString("\n" + dimStyle.Render("no agent selected") + "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n  settings · %s      %s %s %s\n", a.Name,
		buttonView("start", focused("start")), buttonView("stop", focused("stop")), buttonView("remove", focused("remove")))
	hit(*row+1, 34, 46, "start", "")
	hit(*row+1, 48, 60, "stop", "")
	hit(*row+1, 62, 78, "remove", "")
	*row += 2

	b.WriteString("  " + cyclerView(agentTypes[f.agentType], focused("type")) + "  " + f.image.View() + " " + f.uid.View() + "\n")
	hit(*row, 2, 20, "type", "")
	hit(*row, 22, 76, "f-image", "")
	hit(*row, 78, 92, "f-uid", "")
	*row++
	b.WriteString("  " + f.cmd.View() + "\n")
	hit(*row, 2, 92, "f-cmd", "")
	*row++
	b.WriteString("  " + buttonView("Apply", focused("apply-config")) + "\n")
	hit(*row, 2, 14, "apply-config", "")
	*row += 2

	b.WriteString("  " + tabActive.Render(" folders ") + "\n")
	*row++
	p := m.data.paths[a.ID]
	// Green dot = the folder is active/assigned (all three rows alike);
	// own folders are marked with a note.
	am := okStyle.Render("● ")
	agentNote := "(AGENTS.md, skills)"
	if p.AgentDirCustom {
		agentNote = "(AGENTS.md, skills · own folder)"
	}
	fmt.Fprintf(&b, "  %s%-8s /agent  ← %s %s %s %s\n", am, "agent", p.AgentDir, dimStyle.Render(agentNote),
		buttonView("set folder", focused("folder-agent")), buttonView("reset", focused("folder-reset")))
	hit(*row, 2, 66, "folder-agent", "")
	hit(*row, 68, 80, "folder-reset", "")
	*row++
	mm := "   "
	if p.MemoryID != "" {
		mm = okStyle.Render("● ")
	}
	mp := p.MemoryDir
	memNote := "(long-term memory)"
	if mp == "" {
		mp = "—"
	} else if p.MemoryCustom {
		memNote = "(long-term memory · own folder)"
	}
	fmt.Fprintf(&b, "  %s%-8s /memory ← %s %s  %s %s\n", mm, "memory", mp, dimStyle.Render(memNote),
		buttonView("detach", focused("mem-detach")), buttonView("change", focused("change")))
	hit(*row, 2, 66, "mem-detach", "")
	hit(*row, 68, 82, "change", "")
	*row++
	for _, w := range m.data.workspaces {
		mark := "   "
		label, action := "assign", "assign:"+w.ID
		if g := m.grantFor(a.ID, w.ID); g != nil {
			mark = okStyle.Render("● ")
			label, action = "unassign", "unassign:"+w.ID
		}
		fmt.Fprintf(&b, "  %s%-8s /workspace/%-12s ← %-24s %s\n", mark, "project", w.Name, w.HostPath,
			buttonView(label, focused(action)))
		hit(*row, 55, 72, action, w.ID)
		*row++
	}

	// Memory stores appear only on demand ([change]).
	if f.showStores {
		*row++
		b.WriteString("  " + tabActive.Render(" memory stores ") + "\n")
		*row++
		for _, mem := range m.data.memories {
			owner := "free"
			if mem.OwnerAgentID != "" {
				for _, ag := range m.data.agents {
					if ag.ID == mem.OwnerAgentID {
						owner = ag.Name
					}
				}
			}
			pth := mem.Path
			if pth == "" {
				pth = "(default layout)"
			}
			fmt.Fprintf(&b, "    %-14s owner: %-12s %-28s %s\n", shortID(mem.ID), owner, pth,
				buttonView("attach", focused("attach:"+mem.ID)))
			hit(*row, 58, 72, "attach:"+mem.ID, mem.ID)
			*row++
		}
	}
	return b.String()
}

// viewSetupNew shows the create-agent form.
func (m *modelTUI) viewSetupNew(row *int, hit func(int, int, int, string, string), focused func(string) bool) string {
	var b strings.Builder
	f := &m.form
	b.WriteString("\n  " + tabActive.Render(" new agent ") + "\n")
	*row++
	b.WriteString("  " + f.name.View() + "   " + cyclerView(agentTypes[f.agentType], focused("type")) + "\n")
	hit(*row, 2, 42, "f-name", "")
	hit(*row, 52, 74, "type", "")
	*row++
	b.WriteString("  " + f.image.View() + "   " + f.uid.View() + "\n")
	hit(*row, 2, 52, "f-image", "")
	hit(*row, 52, 68, "f-uid", "")
	*row++
	b.WriteString("  " + f.cmd.View() + "\n")
	hit(*row, 2, 92, "f-cmd", "")
	*row++
	b.WriteString("  " + buttonView("Create", focused("create")) + "  " + buttonView("Clear", focused("clear")) + "\n")
	hit(*row, 2, 14, "create", "")
	hit(*row, 16, 28, "clear", "")
	return b.String()
}

// viewSetupWorkspaces shows workspace management (register folders).
func (m *modelTUI) viewSetupWorkspaces(row *int, hit func(int, int, int, string, string), focused func(string) bool) string {
	var b strings.Builder
	f := &m.form
	b.WriteString("\n  " + tabActive.Render(" workspaces ") + "\n")
	*row++
	for _, w := range m.data.workspaces {
		users := 0
		for _, g := range m.data.grants {
			if g.WorkspaceID == w.ID && g.Active(time.Now()) {
				users++
			}
		}
		fmt.Fprintf(&b, "  %-16s %-32s %s\n", w.Name, w.HostPath,
			dimStyle.Render(fmt.Sprintf("%d agent(s)", users)))
		*row++
	}
	*row++
	b.WriteString("  " + tabActive.Render(" register workspace ") + "\n")
	*row++
	b.WriteString("  " + f.wsName.View() + "  " + f.wsPath.View() + "\n")
	hit(*row, 2, 52, "f-wsname", "")
	hit(*row, 52, 130, "f-wspath", "")
	*row++
	b.WriteString("  " + buttonView("Register", focused("register")) + "\n")
	hit(*row, 2, 16, "register", "")
	*row += 2
	b.WriteString("  " + tabActive.Render(" register memory folder ") + "\n")
	*row++
	b.WriteString("  " + f.folder.View() + "\n")
	hit(*row, 2, 110, "f-folder", "")
	*row++
	b.WriteString("  " + buttonView("Register memory", focused("apply-memory")) + "\n")
	hit(*row, 2, 26, "apply-memory", "")
	return b.String()
}

func cyclerView(label string, focused bool) string {
	render := "‹ " + label + " ›"
	if focused {
		return tabActive.Render(render)
	}
	return tabIdle.Render(render)
}

// focusAction focuses the first hit with the given action (tests, buttons).
func (m *modelTUI) focusAction(action string) {
	for i, h := range m.form.hits {
		if h.action == action {
			m.applyFocusTo(i)
			return
		}
	}
}

func buttonView(label string, focused bool) string {
	if focused {
		return tabActive.Render(" " + label + " ")
	}
	return dimStyle.Render("[" + label + "]")
}

// ---- update ----

// updateSetup handles keyboard and mouse for the Setup tab. The hit list
// is the single source for click targets and keyboard focus.
func (m *modelTUI) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.form
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.focusIdx = -1
			for _, ti := range f.allInputs() {
				ti.Blur()
			}
			return m, nil
		case "tab", "down":
			m.cycleSetupFocus(1)
			return m, nil
		case "shift+tab", "up":
			m.cycleSetupFocus(-1)
			return m, nil
		case "enter", " ":
			if f.focusIdx >= 0 && f.focusIdx < len(f.hits) {
				return m, m.activate(f.hits[f.focusIdx])
			}
			return m, nil
		case "left", "right":
			if h := f.focusedHit(); h != nil && h.action == "type" {
				m.cycleType(msg.String() == "right")
				return m, nil
			}
			if h := f.focusedHit(); h != nil && h.action == "target" {
				m.form.folderTarget = (m.form.folderTarget + 1) % 2
				return m, nil
			}
			return m, nil
		}
		if ti := f.focusedInput(); ti != nil {
			var cmd tea.Cmd
			*ti, cmd = ti.Update(msg)
			f.touched[strings.TrimPrefix(f.fieldOf(f.focusIdx), "f-")] = true
			return m, cmd
		}
		return m, nil
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		for i, h := range f.hits {
			if h.y == msg.Y && msg.X >= h.x0 && msg.X < h.x1 {
				f.focusIdx = i
				m.applyFocusTo(i)
				return m, m.activate(h)
			}
		}
	}
	return m, nil
}

func (f *setupForm) allInputs() []*textinput.Model {
	return []*textinput.Model{&f.name, &f.image, &f.cmd, &f.uid, &f.folder, &f.wsName, &f.wsPath}
}

func (f *setupForm) focusedHit() *setupHit {
	if f.focusIdx >= 0 && f.focusIdx < len(f.hits) {
		return &f.hits[f.focusIdx]
	}
	return nil
}

// fieldOf maps a hit index to its field key ("" for non-fields).
func (f *setupForm) fieldOf(idx int) string {
	if idx < 0 || idx >= len(f.hits) {
		return ""
	}
	return f.hits[idx].action
}

// focusedInput returns the text input of the focused hit, if any.
func (f *setupForm) focusedInput() *textinput.Model {
	h := f.focusedHit()
	if h == nil {
		return nil
	}
	switch h.action {
	case "f-name":
		return &f.name
	case "f-image":
		return &f.image
	case "f-cmd":
		return &f.cmd
	case "f-uid":
		return &f.uid
	case "f-folder":
		return &f.folder
	case "f-wsname":
		return &f.wsName
	case "f-wspath":
		return &f.wsPath
	}
	return nil
}

// applyFocusTo focuses the text input of hit idx (blurs all others).
func (m *modelTUI) applyFocusTo(idx int) {
	f := &m.form
	for _, ti := range f.allInputs() {
		ti.Blur()
	}
	f.focusIdx = idx
	if ti := f.focusedInput(); ti != nil {
		ti.Focus()
	}
}

// cycleSetupFocus moves keyboard focus through the hit list.
func (m *modelTUI) cycleSetupFocus(dir int) {
	f := &m.form
	n := len(f.hits)
	if n == 0 {
		return
	}
	idx := f.focusIdx
	for i := 0; i < n; i++ {
		idx = (idx + dir + n) % n
		if idx < 0 {
			idx = n - 1
		}
		break
	}
	m.applyFocusTo(idx)
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

// loadAgentConfig fills the settings fields from the selected agent.
func (m *modelTUI) loadAgentConfig() {
	a := m.selectedAgent()
	if a == nil {
		return
	}
	f := &m.form
	f.image.SetValue(a.Image)
	uid := strconv.Itoa(a.UID)
	if a.UID == 0 {
		uid = "1000"
	}
	f.uid.SetValue(uid)
	cmd := strings.Join(a.Command, " ")
	if len(a.Command) == 3 && a.Command[0] == "sh" && a.Command[1] == "-c" {
		cmd = a.Command[2]
	}
	f.cmd.SetValue(cmd)
	for i, t := range agentTypes {
		if t == a.Type {
			f.agentType = i
		}
	}
	for _, k := range []string{"image", "cmd", "uid"} {
		delete(f.touched, k)
	}
}

// activate performs the action of a focused/clicked control.
func (m *modelTUI) activate(h setupHit) tea.Cmd {
	f := &m.form
	switch {
	case strings.HasPrefix(h.action, "select:"):
		id := strings.TrimPrefix(h.action, "select:")
		for i, a := range m.data.agents {
			if a.ID == id {
				m.cursor = i
			}
		}
		f.mode = 0
		m.loadAgentConfig()
		return nil
	case h.action == "mode:1":
		f.mode = 1
		m.applyTypeDefaults()
		return nil
	case h.action == "mode:2":
		f.mode = 2
		return nil
	case h.action == "change":
		f.showStores = !f.showStores
		return nil
	case h.action == "apply-config":
		return m.submitAgentUpdate()
	case h.action == "start" || h.action == "stop" || h.action == "remove":
		return m.submitAgentAction(h.action)
	case h.action == "apply-memory":
		return m.submitMemoryRegister()
	case h.action == "type":
		m.cycleType(true)
		return nil
	case h.action == "target":
		f.folderTarget = (f.folderTarget + 1) % 2
		return nil
	case h.action == "create":
		return m.submitAgent()
	case h.action == "clear":
		m.clearCreateForm()
		m.status = ""
		return nil
	case h.action == "register":
		return m.submitWorkspace()
	case h.action == "folder-agent":
		// Focus the shared folder input with target "/agent".
		f.folderTarget = 0
		m.focusAction("f-folder")
		return nil
	case h.action == "folder-reset":
		return m.submitFolder("")
	case h.action == "mem-detach":
		return m.submitDetach()
	case h.action == "apply-folder":
		return m.submitFolder(strings.TrimSpace(f.folder.Value()))
	case strings.HasPrefix(h.action, "assign:"):
		return m.submitAssign(strings.TrimPrefix(h.action, "assign:"), "rw")
	case strings.HasPrefix(h.action, "unassign:"):
		return m.submitUnassign(strings.TrimPrefix(h.action, "unassign:"))
	case strings.HasPrefix(h.action, "attach:"):
		return m.submitAttach(strings.TrimPrefix(h.action, "attach:"))
	case strings.HasPrefix(h.action, "f-"):
		m.focusAction(h.action)
		return nil
	}
	return nil
}

// ---- actions ----

// submitAgent creates the agent from the form values.
func (m *modelTUI) submitAgent() tea.Cmd {
	f := &m.form
	name := strings.TrimSpace(f.name.Value())
	image := strings.TrimSpace(f.image.Value())
	if name == "" || image == "" {
		m.status = "create agent: name and image are required"
		return nil
	}
	spec := sdk.AgentSpec{Name: name, Type: agentTypes[f.agentType], Image: image}
	if c := strings.TrimSpace(f.cmd.Value()); c != "" {
		spec.Command = []string{"sh", "-c", c}
	}
	if u := strings.TrimSpace(f.uid.Value()); u != "" {
		if n, err := strconv.Atoi(u); err == nil {
			spec.UID, spec.GID = n, n
		}
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.CreateAgent(ctx, spec)
		return setupDoneMsg{err: err}
	}
}

// submitAgentUpdate saves the changed configuration (restart to apply).
func (m *modelTUI) submitAgentUpdate() tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	f := &m.form
	uid, _ := strconv.Atoi(strings.TrimSpace(f.uid.Value()))
	cmd := strings.TrimSpace(f.cmd.Value())
	specCmd := []string(nil)
	if cmd != "" {
		specCmd = []string{"sh", "-c", cmd}
	}
	client, id := m.client, a.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.UpdateAgent(ctx, id, agentTypes[f.agentType], strings.TrimSpace(f.image.Value()), specCmd, uid, uid)
		return setupDoneMsg{err: err}
	}
}

// submitAgentAction starts/stops/removes the selected agent.
func (m *modelTUI) submitAgentAction(action string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	client, id := m.client, a.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var err error
		switch action {
		case "start":
			_, err = client.AgentAction(ctx, id, "start")
		case "stop":
			err = client.StopAgent(ctx, id)
		case "remove":
			err = client.RemoveAgent(ctx, id, false, false)
		}
		return setupDoneMsg{err: err}
	}
}

// submitMemoryRegister registers the folder input as a memory store.
func (m *modelTUI) submitMemoryRegister() tea.Cmd {
	path := strings.TrimSpace(m.form.folder.Value())
	if path == "" {
		m.status = "register memory: path is required"
		return nil
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.RegisterMemory(ctx, path)
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

// submitFolder sets the /agent folder (target "agent") or registers a new
// memory folder (target "memory"). An empty path resets /agent.
func (m *modelTUI) submitFolder(path string) tea.Cmd {
	a := m.selectedAgent()
	client := m.client
	if m.form.folderTarget == 0 {
		if a == nil {
			m.status = "no agent selected"
			return nil
		}
		id := a.ID
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := client.SetAgentDir(ctx, id, path)
			return setupDoneMsg{err: err}
		}
	}
	if path == "" {
		m.status = "register memory: path is required"
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.RegisterMemory(ctx, path)
		return setupDoneMsg{err: err}
	}
}

// submitDetach unbinds the agent's memory (data is kept).
func (m *modelTUI) submitDetach() tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	client, id := m.client, a.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DetachMemory(ctx, id)
		return setupDoneMsg{err: err}
	}
}

// submitAttach binds a memory store to the selected agent (single writer):
// the current binding is detached first when needed.
func (m *modelTUI) submitAttach(memoryID string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	client, agentID := m.client, a.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if cur, err := client.AgentPaths(ctx, agentID); err == nil && cur.MemoryID != "" && cur.MemoryID != memoryID {
			_, _ = client.DetachMemory(ctx, agentID)
		}
		_, err := client.AttachMemory(ctx, agentID, memoryID)
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
		return setupDoneMsg{err: client.RevokeGrant(ctx, id, false)}
	}
}
