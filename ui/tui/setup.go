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
	// All presets start in YOLO mode (no approval prompts): the DockWeave
	// container *is* the sandbox (non-root, CapDrop ALL, no-new-privileges).
	"codex": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @openai/codex && exec "$HOME/.local/bin/codex" --dangerously-bypass-approvals-and-sandbox`,
	},
	"opencode": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" opencode-ai && exec "$HOME/.local/bin/opencode" --auto`,
	},
	"claude": {
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @anthropic-ai/claude-code && exec "$HOME/.local/bin/claude" --dangerously-skip-permissions`,
	},
	"antigravity": {
		// Google's current coding agent (gemini-cli is legacy). Official
		// installer from antigravity.google/docs/getting-started; the
		// binary lands in $HOME/.local/bin/agy (= /memory, persistent).
		image:   "node:22-bookworm",
		command: `curl -fsSL https://antigravity.google/cli/install.sh | bash && exec "$HOME/.local/bin/agy" --dangerously-skip-permissions`,
	},
	"gemini": {
		// Legacy (Google: "Migrating from Gemini CLI").
		image:   "node:22-bookworm",
		command: `npm install -g --prefix "$HOME/.local" @google/gemini-cli && exec "$HOME/.local/bin/gemini" --yolo`,
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
	agentType             int    // index into agentTypes
	folderTarget          int    // 0 = /agent folder, 1 = new memory folder
	mode                  int    // 0 = agent settings, 1 = new agent, 2 = workspaces
	showStores            bool   // memory store list expanded
	folderOpen            bool   // the set-folder input is visible
	editing               bool   // a field is being changed (typing/cycling)
	savedValue            string // value before editing (esc restores)
	focus                 string // action id of the selected control (stable)
	focusIdx              int    // index into hits (order), -1 = none
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

// setupDoneMsg reports the outcome of a lifecycle/setup action.
type setupDoneMsg struct {
	err  error
	what string // e.g. "started", "stopped" — shown in the status line
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
		return f.focus == action
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
		b.WriteString(m.viewSetupNew(&row, hit, focused))
	case 2:
		b.WriteString(m.viewSetupWorkspaces(&row, hit, focused))
	default:
		b.WriteString(m.viewSetupSettings(&row, hit, focused))
	}
	b.WriteString(m.setupHintLine())
	return b.String()
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
	b.WriteString(fieldView(&f.cmd, focused("f-cmd")) + "\n")
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
	fmt.Fprintf(&b, "  %s%-8s /memory ← %s %s  %s %s %s\n", mm, "memory", mp, dimStyle.Render(memNote),
		buttonView("detach", focused("mem-detach")), buttonView("change", focused("change")),
		buttonView("use own", focused("folder-memory")))
	hit(*row, 2, 66, "mem-detach", "")
	hit(*row, 68, 82, "change", "")
	hit(*row, 84, 100, "folder-memory", "")
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

	// The set-folder input appears on demand ([set folder]/[use own]).
	if f.folderOpen {
		*row++
		b.WriteString("  " + tabActive.Render(" set folder ") + "\n")
		*row++
		targets := []string{"agent", "memory"}
		b.WriteString("  folder for " + cyclerView(targets[f.folderTarget], focused("target")) + "  " + fieldView(&f.folder, focused("f-folder")) + "\n")
		hit(*row, 2, 24, "target", "")
		hit(*row, 26, 110, "f-folder", "")
		*row++
		b.WriteString("  " + buttonView("Apply", focused("apply-folder")) + "  " + buttonView("cancel", focused("folder-cancel")) + "\n")
		hit(*row, 2, 14, "apply-folder", "")
		hit(*row, 16, 30, "folder-cancel", "")
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
	b.WriteString(fieldView(&f.name, focused("f-name")) + "   " + cyclerView(agentTypes[f.agentType], focused("type")) + "\n")
	hit(*row, 2, 42, "f-name", "")
	hit(*row, 52, 74, "type", "")
	*row++
	b.WriteString(fieldView(&f.image, focused("f-image")) + "   " + fieldView(&f.uid, focused("f-uid")) + "\n")
	hit(*row, 2, 52, "f-image", "")
	hit(*row, 52, 68, "f-uid", "")
	*row++
	b.WriteString(fieldView(&f.cmd, focused("f-cmd")) + "\n")
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
	b.WriteString(fieldView(&f.wsName, focused("f-wsname")) + "  " + fieldView(&f.wsPath, focused("f-wspath")) + "\n")
	hit(*row, 2, 52, "f-wsname", "")
	hit(*row, 52, 130, "f-wspath", "")
	*row++
	b.WriteString("  " + buttonView("Register", focused("register")) + "\n")
	hit(*row, 2, 16, "register", "")
	*row += 2
	b.WriteString("  " + tabActive.Render(" register memory folder ") + "\n")
	*row++
	b.WriteString(fieldView(&f.folder, focused("f-folder")) + "\n")
	hit(*row, 2, 110, "f-folder", "")
	*row++
	b.WriteString("  " + buttonView("Register memory", focused("apply-memory")) + "\n")
	hit(*row, 2, 26, "apply-memory", "")
	return b.String()
}

// setupHintLine renders the action hints for the current selection.
func (m *modelTUI) setupHintLine() string {
	h := m.form.focusedHit()
	marker := dimStyle.Render("  · ")
	if h != nil {
		marker = tabActive.Render(" ▸ ")
	}
	return "\n" + marker + dimStyle.Render(m.setupHint(h))
}

func cyclerView(label string, focused bool) string {
	if focused {
		// Width-stable: the ▸ marker replaces the opening bracket.
		return tabActive.Render("▸ " + label + " ›")
	}
	return tabIdle.Render("‹ " + label + " ›")
}

// focusAction selects the first hit with the given action (tests, buttons).
func (m *modelTUI) focusAction(action string) {
	for i, h := range m.form.hits {
		if h.action == action {
			m.applyFocusTo(i)
			return
		}
	}
	m.form.focus = action // not rendered yet: still mark it selected
}

// fieldView renders a text input with an unmissable selection marker:
// the marker and highlight replace the leading indent (same width).
func fieldView(ti *textinput.Model, selected bool) string {
	v := ti.View()
	if selected {
		return tabActive.Render("▸ ") + v
	}
	return "  " + v
}

func buttonView(label string, focused bool) string {
	if focused {
		// Width-stable: the ▸ marker replaces the closing bracket.
		return tabActive.Render("▸" + label + " ")
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
		// Interaction model: cursor keys move the selection, enter starts
		// changing the selected control (cycle lists or type a value),
		// enter again confirms, esc cancels.
		switch msg.String() {
		case "esc":
			if f.editing {
				m.cancelEdit()
			} else {
				f.focusIdx = -1
				for _, ti := range f.allInputs() {
					ti.Blur()
				}
			}
			return m, nil
		case "up":
			if f.editing {
				return m, m.editStep(false)
			}
			m.cycleSetupFocus(-1)
			return m, nil
		case "down":
			if f.editing {
				return m, m.editStep(true)
			}
			m.cycleSetupFocus(1)
			return m, nil
		case "enter":
			if f.editing {
				m.finishEdit()
				return m, nil
			}
			if h := f.focusedHit(); h != nil {
				if strings.HasPrefix(h.action, "f-") || h.action == "type" || h.action == "target" {
					// Enter starts changing the control: cycle lists or
					// type a value, enter confirms, esc cancels.
					m.startEdit()
					return m, nil
				}
				return m, m.activate(*h)
			}
			return m, nil
		case "left", "right":
			if f.editing {
				return m, m.editStep(msg.String() == "right")
			}
			return m, nil
		}
		if f.editing {
			if ti := f.focusedInput(); ti != nil {
				var cmd tea.Cmd
				*ti, cmd = ti.Update(msg)
				f.touched[strings.TrimPrefix(f.fieldOf(f.focusIdx), "f-")] = true
				return m, cmd
			}
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
				if strings.HasPrefix(h.action, "f-") || h.action == "type" || h.action == "target" {
					// Direct manipulation: clicking a changeable control
					// starts editing it right away.
					m.startEdit()
					return m, nil
				}
				return m, m.activate(h)
			}
		}
	}
	return m, nil
}

func (f *setupForm) allInputs() []*textinput.Model {
	return []*textinput.Model{&f.name, &f.image, &f.cmd, &f.uid, &f.folder, &f.wsName, &f.wsPath}
}

// focusedHit returns the focused control from the last render's hit list.
func (f *setupForm) focusedHit() *setupHit {
	return f.focusedHitByID()
}

// focusedHitByID returns the hit with the focused action.
func (f *setupForm) focusedHitByID() *setupHit {
	for i := range f.hits {
		if f.hits[i].action == f.focus {
			return &f.hits[i]
		}
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

// applyFocusTo selects the hit idx (blurs all text inputs first).
func (m *modelTUI) applyFocusTo(idx int) {
	f := &m.form
	for _, ti := range f.allInputs() {
		ti.Blur()
	}
	f.focusIdx = idx
	f.focus = ""
	if idx >= 0 && idx < len(f.hits) {
		f.focus = f.hits[idx].action
	}
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

// startEdit enters change mode for the focused control (fields and
// cyclers): the current value is saved so esc can restore it.
func (m *modelTUI) startEdit() {
	f := &m.form
	h := f.focusedHit()
	if h == nil {
		return
	}
	switch {
	case strings.HasPrefix(h.action, "f-"):
		if ti := f.focusedInput(); ti != nil {
			f.savedValue = ti.Value()
			ti.Focus()
			f.editing = true
		}
	case h.action == "type", h.action == "target":
		if h.action == "type" {
			f.savedValue = agentTypes[f.agentType]
		}
		f.editing = true
	}
}

// finishEdit confirms the change.
func (m *modelTUI) finishEdit() {
	m.form.editing = false
	for _, ti := range m.form.allInputs() {
		ti.Blur()
	}
	m.applyFocusTo(m.form.focusIdx)
}

// cancelEdit aborts the change and restores the previous value.
func (m *modelTUI) cancelEdit() {
	f := &m.form
	h := f.focusedHit()
	if h != nil {
		switch {
		case strings.HasPrefix(h.action, "f-"):
			if ti := f.focusedInput(); ti != nil {
				ti.SetValue(f.savedValue)
			}
		case h.action == "type":
			for i, t := range agentTypes {
				if t == f.savedValue {
					f.agentType = i
				}
			}
		}
	}
	m.finishEdit()
}

// editStep changes list values while editing a cycler (cursor keys).
func (m *modelTUI) editStep(next bool) tea.Cmd {
	f := &m.form
	h := f.focusedHit()
	if h == nil {
		return nil
	}
	switch h.action {
	case "type":
		m.cycleType(next)
	case "target":
		f.folderTarget = (f.folderTarget + 1) % 2
	}
	return nil
}

// setupHint describes the actions available for the selected control.
func (m *modelTUI) setupHint(h *setupHit) string {
	if h == nil {
		return "↑↓: move • enter: change • tab: menu • esc: back"
	}
	base := "↑↓: move • tab: menu"
	switch {
	case h.action == "type" || h.action == "target":
		if m.form.editing {
			return "←→/↑↓: choose • enter: done • esc: cancel • " + base
		}
		return "enter: change (←→/↑↓: choose) • " + base
	case strings.HasPrefix(h.action, "f-"):
		if m.form.editing {
			return "type: value • enter: done • esc: cancel • " + base
		}
		return "enter: edit • type: value • " + base
	default:
		return "enter: run • " + base
	}
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
		// Open the shared folder input for the /agent source.
		f.folderTarget = 0
		f.folderOpen = true
		_ = m.viewSetup() // rebuild hits so the input is focusable now
		m.focusAction("f-folder")
		m.startEdit()
		return nil
	case h.action == "folder-memory":
		// Open the shared folder input for registering an own memory folder.
		f.folderTarget = 1
		f.folderOpen = true
		_ = m.viewSetup() // rebuild hits so the input is focusable now
		m.focusAction("f-folder")
		m.startEdit()
		return nil
	case h.action == "folder-cancel":
		f.folderOpen = false
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

// lifecycle runs a start/stop/restart/remove for the selected agent with
// visible feedback in the status line.
func (m *modelTUI) lifecycle(action string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		m.status = "no agent selected"
		return nil
	}
	m.status = action + " " + a.Name + " …"
	return m.submitAgentAction(action)
}

// submitAgentAction starts/stops/restarts/removes the selected agent.
func (m *modelTUI) submitAgentAction(action string) tea.Cmd {
	a := m.selectedAgent()
	if a == nil {
		return nil
	}
	client, id, name := m.client, a.ID, a.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		var err error
		switch action {
		case "start":
			_, err = client.AgentAction(ctx, id, "start")
		case "stop":
			err = client.StopAgent(ctx, id)
		case "restart":
			_, err = client.AgentAction(ctx, id, "restart")
		case "remove":
			err = client.RemoveAgent(ctx, id, false, false)
		}
		what := map[string]string{"start": "started", "stop": "stopped",
			"restart": "restarted", "remove": "removed"}[action]
		if err != nil {
			return setupDoneMsg{err: err, what: action + " " + name}
		}
		return setupDoneMsg{what: what + " " + name}
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
