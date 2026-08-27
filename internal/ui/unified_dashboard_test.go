package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func TestUnifiedDashboardActionsCannotLaunchAnotherScreen(t *testing.T) {
	src, err := os.ReadFile("home.go")
	if err != nil {
		t.Fatalf("read home.go: %v", err)
	}
	text := string(src)
	for _, signature := range []string{
		"func (h *Home) handleMainKey(",
		"func (h *Home) handleMouse(",
		"func (h *Home) activateSelectedInPlace(",
	} {
		body := funcBody(t, text, signature)
		for _, forbidden := range []string{"tea.Exec(", "attachSession(", "attachRemoteSession("} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contains %s; dashboard action can leave the unified screen", signature, forbidden)
			}
		}
	}
	for _, forbidden := range []string{"OpenSessionInNewWindow", "OpenSessionInSplitPane"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("home dashboard retains native terminal launcher %s", forbidden)
		}
	}
}

func TestUnifiedDashboardSessionHotkeysOpenOverlaysInPlace(t *testing.T) {
	t.Run("new session", func(t *testing.T) {
		setXDGTestHome(t)
		home, _, _ := armHomeWithOneSession(t)

		model, cmd := home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		got := model.(*Home)
		if cmd != nil {
			t.Fatal("n opened an external command instead of the dashboard dialog")
		}
		if got != home || !got.newDialog.IsVisible() || got.isAttaching.Load() {
			t.Fatalf("n did not stay in the unified dashboard: same=%v visible=%v attaching=%v",
				got == home, got.newDialog.IsVisible(), got.isAttaching.Load())
		}
	})

	t.Run("mcp manager", func(t *testing.T) {
		setXDGTestHome(t)
		home, _, _ := armHomeWithOneSession(t)

		model, cmd := home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
		got := model.(*Home)
		if cmd != nil {
			t.Fatal("m opened an external command instead of the dashboard overlay")
		}
		if got != home || !got.mcpDialog.IsVisible() || got.isAttaching.Load() {
			t.Fatalf("m did not stay in the unified dashboard: same=%v visible=%v attaching=%v",
				got == home, got.mcpDialog.IsVisible(), got.isAttaching.Load())
		}
	})
}

func TestUnifiedDashboardClickingOutputActivatesSelectedSession(t *testing.T) {
	home := NewHome()
	home.width = 120
	home.height = 40
	home.initialLoading = false
	home.previewOrientation = PreviewOrientationRight
	home.insertOpenKeySender = func(insertTargetRef) (insertKeySender, error) {
		return &fakeInsertKeySender{}, nil
	}
	remote := session.RemoteSessionInfo{ID: "remote-1", Title: "remote agent"}
	home.flatItems = []session.Item{{
		Type:          session.ItemTypeRemoteSession,
		RemoteName:    "lab",
		RemoteSession: &remote,
	}}
	home.cursor = 0

	model, _ := home.handleMouse(tea.MouseMsg{
		X:      home.width - 2,
		Y:      8,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	got := model.(*Home)
	if !got.insertMode || got.insertModeRemoteName != "lab" || got.insertModeRemoteID != "remote-1" {
		t.Fatalf("Output click did not activate selected session: active=%v remote=%q id=%q",
			got.insertMode, got.insertModeRemoteName, got.insertModeRemoteID)
	}
}

func TestUnifiedDashboardRemoteCreationActivatesOutputAfterRefresh(t *testing.T) {
	home := NewHome()
	home.width = 120
	home.height = 40
	home.initialLoading = false
	home.previewOrientation = PreviewOrientationRight
	home.insertOpenKeySender = func(insertTargetRef) (insertKeySender, error) {
		return &fakeInsertKeySender{}, nil
	}
	home.remoteGroupsCollapsed["remotes/lab"] = true

	model, refreshCmd := home.Update(remoteSessionCreatedMsg{remoteName: "lab", sessionID: "new-remote"})
	home = model.(*Home)
	if refreshCmd == nil {
		t.Fatal("remote creation did not schedule an in-dashboard list refresh")
	}
	remote := session.RemoteSessionInfo{ID: "new-remote", Title: "new agent", Group: "work"}
	model, _ = home.Update(remoteSessionsFetchedMsg{
		sessions: map[string][]session.RemoteSessionInfo{"lab": {remote}},
	})
	home = model.(*Home)

	if !home.insertMode || home.insertModeRemoteName != "lab" || home.insertModeRemoteID != "new-remote" {
		t.Fatalf("created remote session did not open in Output: active=%v remote=%q id=%q",
			home.insertMode, home.insertModeRemoteName, home.insertModeRemoteID)
	}
	if home.remoteGroupsCollapsed["remotes/lab"] {
		t.Fatal("created remote session remained hidden under a collapsed remote")
	}
}
