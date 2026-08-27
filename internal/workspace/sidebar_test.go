package workspace

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

type fakePaneController struct {
	shown     []string
	activated []string
	detached  int
	chrome    [][2]string
}

func (f *fakePaneController) ShowInstance(_ context.Context, id string) error {
	f.shown = append(f.shown, id)
	return nil
}

func (f *fakePaneController) ActivateInstance(_ context.Context, id string) error {
	f.activated = append(f.activated, id)
	return nil
}

func (f *fakePaneController) FocusAgent(context.Context) error { return nil }
func (f *fakePaneController) Detach(context.Context) error {
	f.detached++
	return nil
}
func (f *fakePaneController) UpdateChrome(_ context.Context, header, filters string) error {
	f.chrome = append(f.chrome, [2]string{header, filters})
	return nil
}

func testSidebarItems() []sidebarItem {
	instances := []*session.InstanceData{
		{ID: "a", Title: "Alpha", Tool: "codex", Status: session.StatusRunning, ProjectPath: "/work/alpha", TmuxSession: "agentdeck_a"},
		{ID: "b", Title: "Beta", Tool: "claude", Status: session.StatusWaiting, ProjectPath: "/work/beta", TmuxSession: "agentdeck_b"},
		{ID: "c", Title: "Gamma", Tool: "shell", Status: session.StatusStopped, ProjectPath: "/work/gamma"},
	}
	items := make([]sidebarItem, 0, len(instances))
	for _, inst := range instances {
		items = append(items, sidebarItem{data: inst, attachKey: attachKey(inst)})
	}
	return items
}

func TestSidebarStaleSwitchCannotOverrideLatestHighlight(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	model.generation = 2

	_, staleCmd := model.Update(switchRequestMsg{generation: 1, instanceID: "b", attachKey: "old"})
	if staleCmd != nil {
		t.Fatal("stale generation produced a switch command")
	}
	_, latestCmd := model.Update(switchRequestMsg{generation: 2, instanceID: "c", attachKey: "new"})
	if latestCmd == nil {
		t.Fatal("latest generation did not produce a switch command")
	}
	finished := latestCmd()
	model.Update(finished)
	if got := strings.Join(controller.shown, ","); got != "c" {
		t.Fatalf("viewer switches = %q, want c", got)
	}
}

func TestSidebarEnterAtomicallyActivatesHighlightedSession(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	model.cursor = 1
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not produce an activation command")
	}
	model.Update(cmd())
	if got := strings.Join(controller.activated, ","); got != "b" {
		t.Fatalf("activated = %q, want b", got)
	}
	if len(controller.shown) != 0 {
		t.Fatalf("Enter used non-atomic highlight path: %v", controller.shown)
	}
}

func TestSidebarEnterKeepsFocusForStoppedSession(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	model.cursor = 2
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter on a stopped session must not produce a focus command")
	}
	if len(controller.activated) != 0 {
		t.Fatalf("stopped session was activated: %v", controller.activated)
	}
}

func TestSessionCanAcceptFocus(t *testing.T) {
	tests := []struct {
		name string
		inst *session.InstanceData
		want bool
	}{
		{name: "running", inst: &session.InstanceData{Status: session.StatusRunning, TmuxSession: "agentdeck_a"}, want: true},
		{name: "waiting", inst: &session.InstanceData{Status: session.StatusWaiting, TmuxSession: "agentdeck_a"}, want: true},
		{name: "stopped", inst: &session.InstanceData{Status: session.StatusStopped, TmuxSession: "agentdeck_a"}},
		{name: "missing tmux", inst: &session.InstanceData{Status: session.StatusRunning}},
		{name: "remote", inst: &session.InstanceData{Status: session.StatusRunning, TmuxSession: "agentdeck_a", SSHHost: "host"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionCanAcceptFocus(tt.inst); got != tt.want {
				t.Fatalf("sessionCanAcceptFocus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSidebarStatusChangesDoNotReconnectNativeClient(t *testing.T) {
	inst := &session.InstanceData{ID: "a", TmuxSession: "agentdeck_a", Status: session.StatusRunning}
	before := attachKey(inst)
	inst.Status = session.StatusWaiting
	if after := attachKey(inst); after != before {
		t.Fatalf("status-only change altered attach key: %q != %q", after, before)
	}
	inst.TmuxSession = "agentdeck_a_restarted"
	if after := attachKey(inst); after == before {
		t.Fatal("tmux restart did not alter attach key")
	}
}

func TestSidebarRendersAtPlannedWidthWithSeparateMarkers(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	model.width = 32
	model.height = 18
	model.cursor = 1
	model.activeID = "a"
	view := model.View()
	if strings.Contains(view, "Terminal too small") {
		t.Fatal("workspace sidebar rejected its default width")
	}
	if !strings.Contains(view, "Alpha") || !strings.Contains(view, "Beta") {
		t.Fatalf("expected sessions missing from view:\n%s", view)
	}
	if !strings.Contains(view, "●") || !strings.Contains(view, "▸") {
		t.Fatalf("active/cursor markers are not separate:\n%s", view)
	}
}

func TestSidebarRendersManagerStyleSessionTree(t *testing.T) {
	controller := &fakePaneController{}
	items := testSidebarItems()
	model := newSidebarModel("default", controller, items)
	model.width = 32
	model.height = 18
	view := model.View()

	for _, want := range []string{"SESSIONS", "1·▾", "Alpha", "codex", "Beta", "claude"} {
		if !strings.Contains(view, want) {
			t.Fatalf("manager-style tree missing %q:\n%s", want, view)
		}
	}
}

func TestSidebarQDetachesWithoutQuittingModel(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	returned, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if returned == nil || cmd == nil {
		t.Fatal("q should keep the sidebar model alive and issue detach")
	}
	model.Update(cmd())
	if controller.detached != 1 {
		t.Fatalf("detach count = %d, want 1", controller.detached)
	}
}

func TestLegacySidebarManagerKeysDoNotOpenAnotherScreen(t *testing.T) {
	model := newSidebarModel("default", &fakePaneController{}, testSidebarItems())
	for _, key := range []rune{'m', 'n'} {
		_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil {
			t.Fatalf("legacy sidebar key %q launched another screen", key)
		}
		if model.err == nil || !strings.Contains(model.err.Error(), "unified dashboard") {
			t.Fatalf("legacy sidebar key %q did not direct user to unified dashboard: %v", key, model.err)
		}
	}
}

func TestSidebarFiltersMatchManagerShortcuts(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	if len(model.items) != 3 {
		t.Fatalf("all view items = %d, want 3", len(model.items))
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if cmd == nil || len(model.items) != 1 || model.items[0].data.Title != "Beta" {
		t.Fatalf("waiting filter = %#v", model.items)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	if len(model.items) != 3 {
		t.Fatalf("cleared filter items = %d, want 3", len(model.items))
	}
}

func TestSidebarAcceptsBubbleTeaShiftFilterNames(t *testing.T) {
	model := newSidebarModel("default", &fakePaneController{}, testSidebarItems())
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	if len(model.items) != 1 || model.items[0].data.Title != "Alpha" {
		t.Fatalf("running filter = %#v", model.items)
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	if model.filter != workspaceFilterAll {
		t.Fatalf("second running shortcut did not toggle back to all: %q", model.filter)
	}
}

func TestSessionRefreshUpdatesChromeWhileSwitching(t *testing.T) {
	controller := &fakePaneController{}
	model := newSidebarModel("default", controller, testSidebarItems())
	model.activeKey = "stale"
	_, cmd := model.Update(sessionsLoadedMsg{items: testSidebarItems()})
	if cmd == nil {
		t.Fatal("refresh did not schedule work")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); !ok {
		t.Fatalf("refresh command = %T, want tea.BatchMsg with switch and chrome updates", msg)
	}
}

func TestSidebarChromeContainsFleetCountsAndFilters(t *testing.T) {
	model := newSidebarModel("default", &fakePaneController{}, testSidebarItems())
	header, filters := model.chrome()
	for _, want := range []string{"Agent Deck", "1 running", "1 waiting"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q: %s", want, header)
		}
	}
	for _, want := range []string{"ALL", "!@##& filter", "%% open", "^ archived"} {
		if !strings.Contains(filters, want) {
			t.Fatalf("filters missing %q: %s", want, filters)
		}
	}
}

func TestEscapeTmuxTextPreservesLiteralFormatCharacters(t *testing.T) {
	if got := escapeTmuxText("CPU 80% #1"); got != "CPU 80%% ##1" {
		t.Fatalf("escapeTmuxText() = %q", got)
	}
}
