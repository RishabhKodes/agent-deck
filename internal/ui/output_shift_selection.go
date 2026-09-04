package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RishabhKodes/agent-deck/internal/clipboard"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

type outputSelectionPoint struct {
	x int
	y int
}

type outputTextSelection struct {
	start  outputSelectionPoint
	end    outputSelectionPoint
	active bool
	set    bool
}

type outputSelectionCopyResultMsg struct {
	lineCount int
	err       error
}

func (h *Home) clearOutputTextSelection() {
	h.outputSelection = outputTextSelection{}
}

func (h *Home) outputSelectionXBounds() (minX, maxX int, ok bool) {
	switch h.getLayoutMode() {
	case LayoutModeDual:
		left, _ := h.splitPaneWidths()
		return left + paneSeparatorWidth, h.width, true
	case LayoutModeStacked:
		return 0, h.width, true
	default:
		return 0, 0, false
	}
}

func (h *Home) clampOutputSelectionPoint(point outputSelectionPoint) outputSelectionPoint {
	minX, maxX, ok := h.outputSelectionXBounds()
	if !ok {
		return point
	}
	point.x = max(minX, min(point.x, maxX-1))
	point.y = max(0, min(point.y, max(0, len(strings.Split(h.lastRenderedFrame, "\n"))-1)))
	return point
}

// handleOutputShiftSelection implements hold-to-select without disabling mouse
// reporting. Shift is encoded in SGR mouse events, so ordinary wheel events can
// continue to reach the Output history scroller at the same time.
func (h *Home) handleOutputShiftSelection(msg tea.MouseMsg) (bool, tea.Cmd) {
	if !h.insertMode {
		return false, nil
	}

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		h.clearOutputTextSelection()
		return false, nil
	}

	point := h.clampOutputSelectionPoint(outputSelectionPoint{x: msg.X, y: msg.Y})
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Shift {
		if !h.mouseInPreview(msg.X, msg.Y) {
			return false, nil
		}
		h.outputSelection = outputTextSelection{start: point, end: point, active: true, set: true}
		return true, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && !msg.Shift && h.outputSelection.set {
		h.clearOutputTextSelection()
		return false, nil
	}
	if !h.outputSelection.active {
		return false, nil
	}

	switch msg.Action {
	case tea.MouseActionMotion:
		h.outputSelection.end = point
		return true, nil
	case tea.MouseActionRelease:
		h.outputSelection.end = point
		h.outputSelection.active = false
		payload := selectedOutputText(h.lastRenderedFrame, h.outputSelection, h.outputSelectionXBounds)
		if payload == "" {
			h.clearOutputTextSelection()
			return true, nil
		}
		return true, h.copyOutputTextSelection(payload)
	default:
		return true, nil
	}
}

func orderedOutputSelection(selection outputTextSelection) (outputSelectionPoint, outputSelectionPoint) {
	start, end := selection.start, selection.end
	if end.y < start.y || (end.y == start.y && end.x < start.x) {
		start, end = end, start
	}
	return start, end
}

func selectedOutputText(
	frame string,
	selection outputTextSelection,
	bounds func() (int, int, bool),
) string {
	if frame == "" || !selection.set || bounds == nil {
		return ""
	}
	minX, maxX, ok := bounds()
	if !ok || maxX <= minX {
		return ""
	}
	lines := strings.Split(frame, "\n")
	start, end := orderedOutputSelection(selection)
	start.y = max(0, start.y)
	end.y = min(len(lines)-1, end.y)
	if start.y > end.y || start.y >= len(lines) {
		return ""
	}

	out := make([]string, 0, end.y-start.y+1)
	for y := start.y; y <= end.y; y++ {
		from, to := outputSelectionColumns(start, end, y, minX, maxX)
		text := ansi.Strip(ansi.Cut(lines[y], from, to))
		out = append(out, strings.TrimRight(text, " \t"))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

func outputSelectionColumns(start, end outputSelectionPoint, y, minX, maxX int) (int, int) {
	from, to := minX, maxX
	if start.y == end.y {
		from, to = min(start.x, end.x), max(start.x, end.x)+1
	} else {
		if y == start.y {
			from = start.x
		}
		if y == end.y {
			to = end.x + 1
		}
	}
	from = max(minX, min(from, maxX))
	to = max(from, min(to, maxX))
	return from, to
}

func (h *Home) renderOutputTextSelection(frame string) string {
	if frame == "" || !h.outputSelection.set {
		return frame
	}
	minX, maxX, ok := h.outputSelectionXBounds()
	if !ok || maxX <= minX {
		return frame
	}
	lines := strings.Split(frame, "\n")
	start, end := orderedOutputSelection(h.outputSelection)
	start.y = max(0, start.y)
	end.y = min(len(lines)-1, end.y)
	for y := start.y; y <= end.y && y < len(lines); y++ {
		from, to := outputSelectionColumns(start, end, y, minX, maxX)
		if to <= from {
			continue
		}
		prefix := ansi.Cut(lines[y], 0, from)
		selected := ansi.Strip(ansi.Cut(lines[y], from, to))
		suffix := ansi.Cut(lines[y], to, max(h.width, cellWidth(lines[y])))
		lines[y] = prefix + "\x1b[7m" + selected + "\x1b[27m" + suffix
	}
	return strings.Join(lines, "\n")
}

func (h *Home) copyOutputTextSelection(text string) tea.Cmd {
	return func() tea.Msg {
		copyText := h.paneClipboard
		if copyText == nil {
			copyText = clipboard.Copy
		}
		result, err := copyText(text, tmux.GetTerminalInfo().SupportsOSC52)
		if err != nil {
			return outputSelectionCopyResultMsg{err: fmt.Errorf("clipboard: %w", err)}
		}
		return outputSelectionCopyResultMsg{lineCount: result.LineCount}
	}
}
