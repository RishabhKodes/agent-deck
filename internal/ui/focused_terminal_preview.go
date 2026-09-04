package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

// focusedTerminalGeometryMsg is returned after the selected agent's control
// client has been assigned the Output pane's geometry. A fresh capture after
// the resize picks up the application's SIGWINCH redraw.
type focusedTerminalGeometryMsg struct {
	sessionID string
	err       error
}

func (h *Home) isFocusedAgentTerminal(inst *session.Instance) bool {
	return inst != nil &&
		h.insertMode &&
		h.insertModeSessionID == inst.ID
}

// focusedAgentTerminalDimensions returns the exact region passed to
// renderPreviewPane. It mirrors renderDualColumnLayout/renderStackedLayout;
// the single-column layout has no Output pane and therefore no client size.
func (h *Home) focusedAgentTerminalDimensions() (cols, rows int, ok bool) {
	const (
		helpBarHeight   = 2
		filterBarHeight = 1
		panelTitleLines = 2
	)
	maintenanceBannerHeight := 0
	if h.maintenanceMsg != "" {
		maintenanceBannerHeight = 1
	}
	debugBarHeight := 0
	if h.debugMode {
		debugBarHeight = 1
	}
	contentHeight := h.height - 1 - helpBarHeight -
		maintenanceBannerHeight - filterBarHeight - debugBarHeight

	switch h.getLayoutMode() {
	case LayoutModeSingle:
		return 0, 0, false
	case LayoutModeStacked:
		listHeight := h.stackedListHeight(contentHeight)
		previewHeight := contentHeight - listHeight - 1
		cols, rows = h.width, previewHeight-panelTitleLines
	default:
		_, cols = h.splitPaneWidths()
		rows = contentHeight - panelTitleLines
	}
	if cols < 10 || rows < 3 {
		return 0, 0, false
	}
	return cols, rows, true
}

// syncFocusedAgentGeometryCmd publishes geometry only when it changed. The
// control client is already maintained by PipeManager for the selected
// session, so this adds no PTY and no extra long-lived process.
func (h *Home) syncFocusedAgentGeometryCmd() tea.Cmd {
	inst, _, _ := h.selectedPreviewTarget()
	if !h.isFocusedAgentTerminal(inst) {
		h.focusedPreviewGeometrySession = ""
		h.focusedPreviewGeometryCols = 0
		h.focusedPreviewGeometryRows = 0
		return nil
	}
	tmuxSession := inst.GetTmuxSession()
	if tmuxSession == nil {
		return nil
	}
	cols, rows, ok := h.focusedAgentTerminalDimensions()
	if !ok {
		return nil
	}
	if h.focusedPreviewGeometrySession == tmuxSession.Name &&
		h.focusedPreviewGeometryCols == cols && h.focusedPreviewGeometryRows == rows {
		return nil
	}
	h.focusedPreviewGeometrySession = tmuxSession.Name
	h.focusedPreviewGeometryCols = cols
	h.focusedPreviewGeometryRows = rows
	instanceID := inst.ID
	tmuxName := tmuxSession.Name

	return func() tea.Msg {
		pm := tmux.GetPipeManager()
		if pm == nil {
			return focusedTerminalGeometryMsg{sessionID: instanceID}
		}
		return focusedTerminalGeometryMsg{
			sessionID: instanceID,
			err:       pm.SetClientSize(tmuxName, cols, rows),
		}
	}
}

// renderFocusedAgentTerminalAtOffset reproduces the current tmux cell grid
// without Agent Deck metadata or blank-line collapsing. At the live tail it
// can overlay tmux's cursor, which capture-pane intentionally leaves out.
func renderFocusedAgentTerminalAtOffset(
	content string,
	hasCached bool,
	width, height, offset int,
	cursor *tmux.PaneCursor,
	caretVisible bool,
) (string, int) {
	if width < 1 || height < 1 {
		return "", 0
	}
	if !hasCached {
		return lipgloss.NewStyle().Foreground(ColorText).Italic(true).Render("Loading agent terminal..."), 0
	}
	if content == "" {
		return lipgloss.NewStyle().Foreground(ColorText).Italic(true).Render("(terminal is empty)"), 0
	}

	lines := strings.Split(content, "\n")
	// capture-pane prints one terminating newline; it is not an extra terminal
	// row. Every other blank row is part of the application's layout.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	end := len(lines) - offset
	start := end - height
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	lines = lines[start:end]

	cursorRow := -1
	if cursor != nil && cursor.Visible && caretVisible && offset == 0 {
		cursorRow = cursor.Y - start
	}

	for i, line := range lines {
		line = stripControlCharsPreserveANSI(line)
		line = stripDisplayErasingEscapes(line)
		if cellWidth(line) > width {
			line = cellTruncate(line, width, "")
		}
		if i == cursorRow {
			line = overlayTerminalCaret(line, cursor.X, width)
		}
		// Contain captured SGR state at every row boundary so it cannot leak
		// into the dashboard pane beside this one.
		if strings.ContainsRune(line, 0x1b) {
			line += "\x1b[0m"
		}
		lines[i] = line
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), offset
}

func overlayTerminalCaret(line string, x, width int) string {
	if x < 0 || x >= width {
		return line
	}
	prefix := ansi.Cut(line, 0, x)
	if gap := x - cellWidth(prefix); gap > 0 {
		prefix += strings.Repeat(" ", gap)
	}
	suffix := ""
	if cellWidth(line) > x+1 {
		suffix = ansi.Cut(line, x+1, width)
	}
	caret := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Render("▌")
	return prefix + caret + suffix
}
