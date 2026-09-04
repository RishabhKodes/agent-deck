package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParsePaneCursor(t *testing.T) {
	got, err := parsePaneCursor("8|3|1\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 8 || got.Y != 3 || !got.Visible {
		t.Fatalf("cursor = %#v, want x=8 y=3 visible", got)
	}

	hidden, err := parsePaneCursor("0|9|0")
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Visible {
		t.Fatalf("cursor = %#v, want hidden", hidden)
	}
}

func TestParsePaneCursorRejectsMalformedResponse(t *testing.T) {
	for _, input := range []string{"", "1|2", "x|2|1", "1|y|1", "1|2|maybe"} {
		if _, err := parsePaneCursor(input); err == nil {
			t.Fatalf("parsePaneCursor(%q) succeeded, want error", input)
		}
	}
}

func TestControlPipeCapturePaneFrameVia(t *testing.T) {
	name := createTestSessionStrict(t, "pane-frame")
	if err := exec.Command("tmux", "send-keys", "-t", name, "printf frame-probe", "Enter").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)

	pipe, err := NewControlPipe(name, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	frame, err := pipe.CapturePaneFrameVia(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame.Content, "frame-probe") {
		t.Fatalf("frame content = %q, want probe", frame.Content)
	}
	if frame.Cursor.X < 0 || frame.Cursor.Y < 0 || !frame.Cursor.Visible {
		t.Fatalf("cursor = %#v, want non-negative visible cursor", frame.Cursor)
	}
}
