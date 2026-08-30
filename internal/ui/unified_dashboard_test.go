package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
