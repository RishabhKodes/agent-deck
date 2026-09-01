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

func TestOutputActivationKeepsWheelReportingEnabled(t *testing.T) {
	h, _, _ := armHomeWithOneWindowRow(t, "claude", 0)
	h.insertOpenKeySender = func(insertTargetRef) (insertKeySender, error) {
		return &fakeInsertKeySender{}, nil
	}

	_, cmd := h.activateSelectedInPlace()
	if cmd == nil {
		t.Fatal("activating Output returned no command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("activating Output returned %T, want a command batch", msg)
	}
	if got := batch[0](); fmt.Sprintf("%T", got) != fmt.Sprintf("%T", tea.EnableMouseCellMotion()) {
		t.Fatalf("activating Output started with %T, want EnableMouseCellMotion so wheel events remain reachable", got)
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
