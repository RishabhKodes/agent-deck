package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

// codexPreviewGeometryMsg is returned after the focused Codex control client
// has been assigned the Output pane's geometry. A fresh capture after the
// resize picks up Codex's SIGWINCH redraw rather than displaying the stale,
// pre-resize frame.
type codexPreviewGeometryMsg struct {
	sessionID string
	err       error
}

func (h *Home) isFocusedCodexTerminal(inst *session.Instance) bool {
	return inst != nil &&
		h.insertMode &&
		h.insertModeSessionID == inst.ID &&
		session.IsCodexCompatible(inst.Tool)
}

// focusedCodexTerminalDimensions returns the exact region passed to
// renderPreviewPane. It mirrors renderDualColumnLayout/renderStackedLayout;
// the single-column layout has no Output pane and therefore no client size.
func (h *Home) focusedCodexTerminalDimensions() (cols, rows int, ok bool) {
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
		previewHeight := contentHeight - listHeight - 1 // horizontal separator
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

// syncFocusedCodexGeometryCmd publishes geometry only when it changed. tmux's
// control client is already maintained by PipeManager for the selected
// session, so this adds no PTY and no extra long-lived process.
func (h *Home) syncFocusedCodexGeometryCmd() tea.Cmd {
	inst, _, _ := h.selectedPreviewTarget()
	if !h.isFocusedCodexTerminal(inst) {
		h.codexPreviewGeometrySession = ""
		h.codexPreviewGeometryCols = 0
		h.codexPreviewGeometryRows = 0
		return nil
	}
	tmuxSession := inst.GetTmuxSession()
	if tmuxSession == nil {
		return nil
	}
	cols, rows, ok := h.focusedCodexTerminalDimensions()
	if !ok {
		return nil
	}
	if h.codexPreviewGeometrySession == tmuxSession.Name &&
		h.codexPreviewGeometryCols == cols && h.codexPreviewGeometryRows == rows {
		return nil
	}
	h.codexPreviewGeometrySession = tmuxSession.Name
	h.codexPreviewGeometryCols = cols
	h.codexPreviewGeometryRows = rows
	instanceID := inst.ID
	tmuxName := tmuxSession.Name

	return func() tea.Msg {
		pm := tmux.GetPipeManager()
		if pm == nil {
			return codexPreviewGeometryMsg{sessionID: instanceID}
		}
		return codexPreviewGeometryMsg{
			sessionID: instanceID,
			err:       pm.SetClientSize(tmuxName, cols, rows),
		}
	}
}

// renderFocusedCodexTerminal reproduces the current tmux cell grid without
// Agent Deck's metadata, history indicators, or blank-line collapsing. Those
// transformations are useful in a browsing preview but alter a full-screen
// TUI's layout and can remove Codex's top or bottom chrome.
func renderFocusedCodexTerminal(content string, hasCached bool, width, height int) string {
	rendered, _ := renderFocusedCodexTerminalAtOffset(content, hasCached, width, height, 0)
	return rendered
}

// renderFocusedCodexTerminalAtOffset renders a tail-relative historical
// window while retaining the exact live-grid behavior at offset zero. The
// returned offset is clamped to the captured buffer and lets Home retire a
// frozen snapshot when there is not enough history to scroll.
func renderFocusedCodexTerminalAtOffset(content string, hasCached bool, width, height, offset int) (string, int) {
	if width < 1 || height < 1 {
		return "", 0
	}
	if !hasCached {
		return lipgloss.NewStyle().Foreground(ColorText).Italic(true).Render("Loading Codex terminal..."), 0
	}
	if content == "" {
		return lipgloss.NewStyle().Foreground(ColorText).Italic(true).Render("(terminal is empty)"), 0
	}

	lines := strings.Split(content, "\n")
	// capture-pane prints one terminating newline; it is not an extra terminal
	// row. Preserve every other blank row because vertical spacing is part of
	// Codex's screen layout.
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

	isLightTheme := GetCurrentTheme() == ThemeLight
	for i, line := range lines {
		line = stripControlCharsPreserveANSI(line)
		line = stripDisplayErasingEscapes(line)
		if isLightTheme {
			line = remapANSIBackground(line, previewSurfaceANSI())
		}
		if cellWidth(line) > width {
			line = cellTruncate(line, width, "")
		}
		// A capture can end a row while an SGR style remains active. A real
		// terminal retains it until the next draw command; the dashboard joins
		// this block beside its own UI, so contain the style at every row edge.
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
