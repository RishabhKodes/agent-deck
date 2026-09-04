package ui

import tea "github.com/charmbracelet/bubbletea"

// restoreMouseModeAfterExternalScreenCmd restores dashboard mouse reporting.
// Output keeps reporting enabled because both wheel scrolling and Shift-drag
// selection are handled by Agent Deck.
func (h *Home) restoreMouseModeAfterExternalScreenCmd() tea.Cmd {
	return tea.EnableMouseCellMotion
}
