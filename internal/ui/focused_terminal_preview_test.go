package ui

import (
	"strings"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

func TestRenderFocusedAgentTerminalPreservesCurrentScreen(t *testing.T) {
	content := strings.Join([]string{
		"old transcript line",
		"╭─ Agent ─╮",
		"",
		"› composer",
		"model · ? for shortcuts",
		"",
	}, "\n")

	got, _ := renderFocusedAgentTerminalAtOffset(content, true, 80, 4, 0, nil, false)
	got = tmux.StripANSI(got)
	want := strings.Join([]string{
		"╭─ Agent ─╮",
		"",
		"› composer",
		"model · ? for shortcuts",
	}, "\n")
	if got != want {
		t.Fatalf("focused agent terminal changed the current screen\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderFocusedAgentTerminalScrollsCapturedHistory(t *testing.T) {
	content := strings.Join([]string{
		"history-0", "history-1", "history-2", "history-3",
		"live-0", "live-1", "live-2", "live-3", "",
	}, "\n")

	got, offset := renderFocusedAgentTerminalAtOffset(content, true, 80, 4, 2, nil, false)
	want := strings.Join([]string{"history-2", "history-3", "live-0", "live-1"}, "\n")
	if plain := tmux.StripANSI(got); plain != want {
		t.Fatalf("scrolled agent window\n got: %q\nwant: %q", plain, want)
	}
	if offset != 2 {
		t.Fatalf("clamped offset=%d, want 2", offset)
	}

	got, offset = renderFocusedAgentTerminalAtOffset(content, true, 80, 4, 999, nil, false)
	if plain := tmux.StripANSI(got); !strings.Contains(plain, "history-0") || strings.Contains(plain, "live-3") {
		t.Fatalf("oldest agent window was not clamped to captured history: %q", plain)
	}
	if offset != 4 {
		t.Fatalf("oldest clamped offset=%d, want 4", offset)
	}
}

func TestFocusedAgentPreviewSkipsAgentDeckMetadataForEveryProvider(t *testing.T) {
	for _, tool := range []string{"codex", "claude", "cursor", "gemini"} {
		t.Run(tool, func(t *testing.T) {
			inst := session.NewInstanceWithTool(tool+"-focus", t.TempDir(), tool)
			inst.Status = session.StatusRunning
			h := homeWithSession(inst)
			h.insertMode = true
			h.insertModeSessionID = inst.ID
			h.previewCache[inst.ID] = "Agent terminal\n\n› composer\nmodel · ? for shortcuts\n"

			got := tmux.StripANSI(h.renderPreviewPane(80, 4))
			for _, want := range []string{"Agent terminal", "› composer", "? for shortcuts"} {
				if !strings.Contains(got, want) {
					t.Fatalf("focused %s preview omitted %q: %q", tool, want, got)
				}
			}
			for _, unwanted := range []string{inst.Title + "  ", "📁 ", "─ Output ─"} {
				if strings.Contains(got, unwanted) {
					t.Fatalf("focused %s preview retained Agent Deck metadata %q: %q", tool, unwanted, got)
				}
			}
		})
	}
}

func TestFocusedAgentTerminalDimensionsMatchRenderedPane(t *testing.T) {
	h := NewHome()
	h.width = 120
	h.height = 40
	h.previewPct = 65
	h.previewOrientation = PreviewOrientationRight

	cols, rows, ok := h.focusedAgentTerminalDimensions()
	if !ok {
		t.Fatal("dual-column dashboard should expose a terminal region")
	}
	_, wantCols := h.splitPaneWidths()
	if cols != wantCols || rows != 34 {
		t.Fatalf("focused terminal geometry = %dx%d, want %dx34", cols, rows, wantCols)
	}

	h.width = layoutBreakpointSingle - 1
	if _, _, ok := h.focusedAgentTerminalDimensions(); ok {
		t.Fatal("single-column layout has no terminal region to publish")
	}
}

func TestFocusedAgentGeometryPublishesOnlyWhenChanged(t *testing.T) {
	inst := session.NewInstanceWithTool("claude-geometry", t.TempDir(), "claude")
	tmuxSession := tmux.NewSession("claude-geometry", t.TempDir())
	inst.SetTmuxSessionForTest(tmuxSession)
	h := homeWithSession(inst)
	h.insertMode = true
	h.insertModeSessionID = inst.ID

	if cmd := h.syncFocusedAgentGeometryCmd(); cmd == nil {
		t.Fatal("entering focused agent output did not publish terminal geometry")
	}
	cols, rows, ok := h.focusedAgentTerminalDimensions()
	if !ok {
		t.Fatal("test dashboard should expose a terminal region")
	}
	if h.focusedPreviewGeometrySession != tmuxSession.Name ||
		h.focusedPreviewGeometryCols != cols || h.focusedPreviewGeometryRows != rows {
		t.Fatalf("published geometry signature = %q %dx%d, want %q %dx%d",
			h.focusedPreviewGeometrySession, h.focusedPreviewGeometryCols,
			h.focusedPreviewGeometryRows, tmuxSession.Name, cols, rows)
	}
	if cmd := h.syncFocusedAgentGeometryCmd(); cmd != nil {
		t.Fatal("unchanged geometry scheduled another tmux round trip")
	}

	h.width++
	if cmd := h.syncFocusedAgentGeometryCmd(); cmd == nil {
		t.Fatal("viewport resize did not republish terminal geometry")
	}
	h.insertMode = false
	if cmd := h.syncFocusedAgentGeometryCmd(); cmd != nil {
		t.Fatal("leaving agent output scheduled a geometry update")
	}
	if h.focusedPreviewGeometrySession != "" || h.focusedPreviewGeometryCols != 0 || h.focusedPreviewGeometryRows != 0 {
		t.Fatal("leaving agent output did not clear the geometry signature")
	}
}

func TestFocusedAgentTerminalOverlaysVisibleCursor(t *testing.T) {
	cursor := tmux.PaneCursor{X: 4, Y: 1, Visible: true}
	content := "first\nabc value\nthird\n"
	got, _ := renderFocusedAgentTerminalAtOffset(content, true, 20, 3, 0, &cursor, true)
	lines := strings.Split(tmux.StripANSI(got), "\n")
	if !strings.Contains(lines[1], "abc ▌alue") {
		t.Fatalf("cursor overlay missing at tmux coordinates: %q", lines[1])
	}
}
