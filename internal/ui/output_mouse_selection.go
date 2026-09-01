package ui

import tea "github.com/charmbracelet/bubbletea"

// restoreMouseModeAfterExternalScreenCmd restores dashboard mouse reporting
// after a legacy external-screen path. Output keeps reporting enabled too: the
// wheel belongs to Agent Deck's in-pane history viewport, while users retain
// native terminal selection through the terminal-standard Shift-drag override.
func (h *Home) restoreMouseModeAfterExternalScreenCmd() tea.Cmd {
	return tea.EnableMouseCellMotion
}

// disableMouseCmd temporarily releases mouse reporting while the Output
// interaction surface is active, allowing the terminal emulator to perform
// native drag-to-select and copy.
func disableMouseCmd() tea.Cmd {
	return func() tea.Msg { return tea.DisableMouse() }
}
