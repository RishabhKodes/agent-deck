package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RishabhKodes/agent-deck/internal/clipboard"
)

func TestShiftDragSelectsAndCopiesWhileMouseReportingStaysEnabled(t *testing.T) {
	h := NewHome()
	h.width = 120
	h.height = 40
	h.insertMode = true
	left, _ := h.splitPaneWidths()
	startX := left + paneSeparatorWidth + 2
	h.lastRenderedFrame = strings.Repeat("\n", 5) + strings.Repeat(" ", startX) + "select this text"

	press := tea.MouseMsg{X: startX, Y: 5, Shift: true, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	if handled, cmd := h.handleOutputShiftSelection(press); !handled || cmd != nil {
		t.Fatalf("Shift press = handled %v, cmd %v; want true, nil", handled, cmd)
	}
	motion := tea.MouseMsg{X: startX + len("select this text") - 1, Y: 5, Shift: true, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
	if handled, cmd := h.handleOutputShiftSelection(motion); !handled || cmd != nil {
		t.Fatalf("Shift drag = handled %v, cmd %v; want true, nil", handled, cmd)
	}

	var copied string
	h.paneClipboard = func(text string, _ bool) (*clipboard.CopyResult, error) {
		copied = text
		return &clipboard.CopyResult{LineCount: 1}, nil
	}
	release := tea.MouseMsg{X: motion.X, Y: motion.Y, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease}
	handled, cmd := h.handleOutputShiftSelection(release)
	if !handled || cmd == nil {
		t.Fatalf("release = handled %v, cmd %v; want true, non-nil", handled, cmd)
	}
	if msg, ok := cmd().(outputSelectionCopyResultMsg); !ok || msg.err != nil || msg.lineCount != 1 {
		t.Fatalf("copy result = %#v", msg)
	}
	if copied != "select this text" {
		t.Fatalf("copied %q, want selected text", copied)
	}
	if !h.outputSelection.set || h.outputSelection.active {
		t.Fatalf("selection state after release = %+v", h.outputSelection)
	}
	if cmd := h.restoreMouseModeAfterExternalScreenCmd(); !commandContainsMessageType(cmd, tea.EnableMouseCellMotion()) {
		t.Fatal("Shift selection disabled wheel mouse reporting")
	}
}

func TestShiftDragRendersVisibleHighlight(t *testing.T) {
	h := NewHome()
	h.width = 120
	h.height = 40
	left, _ := h.splitPaneWidths()
	x := left + paneSeparatorWidth
	frame := strings.Repeat(" ", x) + "highlight me"
	h.outputSelection = outputTextSelection{
		start: outputSelectionPoint{x: x, y: 0},
		end:   outputSelectionPoint{x: x + len("highlight") - 1, y: 0},
		set:   true,
	}

	got := h.renderOutputTextSelection(frame)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("selected range has no visual style: %q", got)
	}
	if ansi.Strip(got) != frame {
		t.Fatalf("highlight changed visible text: got %q want %q", ansi.Strip(got), frame)
	}
}

func TestOrdinaryWheelStillReachesPreviewDuringShiftSelectionFeature(t *testing.T) {
	h, inst := previewScrollSessionWithLines(t, 120, 40, 80)
	h.insertMode = true
	h.insertModeSessionID = inst.ID

	model, _ := h.Update(tea.MouseMsg{X: 100, Y: 10, Button: tea.MouseButtonWheelUp})
	h = model.(*Home)
	if h.previewScrollOffset != outputWheelScrollLines {
		t.Fatalf("wheel offset=%d, want %d", h.previewScrollOffset, outputWheelScrollLines)
	}
}
