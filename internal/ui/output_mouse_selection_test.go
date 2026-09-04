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

func TestOutputInteractionEnablesMouseForWheelScrolling(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "gemini", "opencode", "shell"} {
		t.Run(tool, func(t *testing.T) {
			h, _, _ := armHomeWithOneWindowRow(t, tool, 0)
			h.insertOpenKeySender = func(insertTargetRef) (insertKeySender, error) {
				return &fakeInsertKeySender{}, nil
			}
			_, cmd := h.activateSelectedInPlace()
			if !commandContainsMessageType(cmd, tea.EnableMouseCellMotion()) {
				t.Fatalf("%s Output did not enable mouse reporting for wheel scrolling", tool)
			}
		})
	}
}

func TestOutputActivationStartsWithWheelScrollingMode(t *testing.T) {
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
		t.Fatalf("activating Output started with %T, want EnableMouseCellMotion for wheel scrolling", got)
	}
}

func TestExternalScreenReturnRestoresStateAppropriateMouseMode(t *testing.T) {
	h := NewHome()
	if cmd := h.restoreMouseModeAfterExternalScreenCmd(); !commandContainsMessageType(cmd, tea.EnableMouseCellMotion()) {
		t.Fatal("navigation mode did not restore dashboard mouse reporting")
	}

	h.insertMode = true
	if cmd := h.restoreMouseModeAfterExternalScreenCmd(); !commandContainsMessageType(cmd, tea.EnableMouseCellMotion()) {
		t.Fatal("Output mode did not restore wheel-scrolling mouse reporting")
	}

}
