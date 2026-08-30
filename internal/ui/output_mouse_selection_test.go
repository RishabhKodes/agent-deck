package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func commandContainsMessageType(cmd tea.Cmd, want tea.Msg) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if commandContainsMessageType(child, want) {
				return true
			}
		}
		return false
	}
	return fmt.Sprintf("%T", msg) == fmt.Sprintf("%T", want)
}

func TestOutputInteractionNeverDisablesDashboardMouse(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "gemini", "opencode", "shell"} {
		t.Run(tool, func(t *testing.T) {
			h := NewHome()
			h.insertMode = true
			_, cmd := h.Update(struct{ tool string }{tool: tool})
			if commandContainsMessageType(cmd, tea.DisableMouse()) {
				t.Fatalf("%s Output disabled mouse reporting and made wheel scrolling unreachable", tool)
			}
		})
	}
}

func TestExternalScreenReturnRestoresMouseDuringOutput(t *testing.T) {
	for _, active := range []bool{false, true} {
		h := NewHome()
		h.insertMode = active
		cmd := h.restoreMouseModeAfterExternalScreenCmd()
		if !commandContainsMessageType(cmd, tea.EnableMouseCellMotion()) {
			t.Fatalf("insertMode=%v: external-screen return did not restore mouse reporting", active)
		}
	}
}
