package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// PaneCursor is the tmux cursor position within a captured pane grid.
// Coordinates are zero-based terminal cells.
type PaneCursor struct {
	X       int
	Y       int
	Visible bool
}

// PaneFrame is a current-screen capture plus the cursor metadata that
// capture-pane itself omits.
type PaneFrame struct {
	Content string
	Cursor  PaneCursor
}

func parsePaneCursor(raw string) (PaneCursor, error) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) != 3 {
		return PaneCursor{}, fmt.Errorf("unexpected tmux cursor response %q", raw)
	}
	x, err := strconv.Atoi(parts[0])
	if err != nil {
		return PaneCursor{}, fmt.Errorf("parse cursor x %q: %w", parts[0], err)
	}
	y, err := strconv.Atoi(parts[1])
	if err != nil {
		return PaneCursor{}, fmt.Errorf("parse cursor y %q: %w", parts[1], err)
	}
	flag, err := strconv.Atoi(parts[2])
	if err != nil {
		return PaneCursor{}, fmt.Errorf("parse cursor flag %q: %w", parts[2], err)
	}
	return PaneCursor{X: x, Y: y, Visible: flag != 0}, nil
}

// CapturePaneFrameVia captures a tmux target through the persistent control
// client and then reads its cursor position. Keeping both operations on the
// same serialized pipe makes this cheap enough for output-driven rendering.
func (cp *ControlPipe) CapturePaneFrameVia(target string) (PaneFrame, error) {
	if strings.TrimSpace(target) == "" {
		target = cp.sessionName
	}
	content, err := cp.SendCommand("capture-pane -t " + tmuxQuote(target) + " -p -e")
	if err != nil {
		return PaneFrame{}, err
	}
	cursorRaw, err := cp.SendCommand(
		"display-message -t " + tmuxQuote(target) + " -p " + tmuxQuote("#{cursor_x}|#{cursor_y}|#{cursor_flag}"),
	)
	if err != nil {
		return PaneFrame{}, err
	}
	cursor, err := parsePaneCursor(cursorRaw)
	if err != nil {
		return PaneFrame{}, err
	}
	return PaneFrame{Content: content, Cursor: cursor}, nil
}
