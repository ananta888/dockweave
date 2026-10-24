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
			{ID: "ag2", Name: "beta", Type: "custom", Image: "img-b", State: model.AgentExited},
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
