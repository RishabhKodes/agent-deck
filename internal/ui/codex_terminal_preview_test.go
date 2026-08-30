package ui

import (
	"strings"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

func TestRenderFocusedCodexTerminalPreservesCurrentScreen(t *testing.T) {
	content := strings.Join([]string{
		"old transcript line",
		"╭─ OpenAI Codex ─╮",
		"",
		"› composer",
		"gpt-5.6 · ? for shortcuts",
		"", // capture-pane's terminating newline
	}, "\n")

	got := tmux.StripANSI(renderFocusedCodexTerminal(content, true, 80, 4))
	want := strings.Join([]string{
		"╭─ OpenAI Codex ─╮",
		"",
		"› composer",
		"gpt-5.6 · ? for shortcuts",
	}, "\n")
	if got != want {
		t.Fatalf("focused Codex terminal changed the current screen\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "more lines above") || strings.Contains(got, "old transcript") {
		t.Fatalf("focused terminal leaked preview/history chrome: %q", got)
	}
}

func TestRenderFocusedCodexTerminalScrollsCapturedHistory(t *testing.T) {
	content := strings.Join([]string{
		"history-0",
		"history-1",
		"history-2",
		"history-3",
		"live-0",
		"live-1",
		"live-2",
		"live-3",
		"", // capture-pane's terminating newline
	}, "\n")

	got, offset := renderFocusedCodexTerminalAtOffset(content, true, 80, 4, 2)
	want := strings.Join([]string{"history-2", "history-3", "live-0", "live-1"}, "\n")
	if plain := tmux.StripANSI(got); plain != want {
		t.Fatalf("scrolled Codex window\n got: %q\nwant: %q", plain, want)
	}
	if offset != 2 {
		t.Fatalf("clamped offset=%d, want 2", offset)
	}

	got, offset = renderFocusedCodexTerminalAtOffset(content, true, 80, 4, 999)
	if plain := tmux.StripANSI(got); !strings.Contains(plain, "history-0") || strings.Contains(plain, "live-3") {
		t.Fatalf("oldest Codex window was not clamped to captured history: %q", plain)
	}
	if offset != 4 {
		t.Fatalf("oldest clamped offset=%d, want 4", offset)
	}
}

func TestFocusedCodexPreviewSkipsAgentDeckMetadata(t *testing.T) {
	inst := session.NewInstanceWithTool("codex-focus", t.TempDir(), "codex")
	inst.Status = session.StatusRunning
	h := homeWithSession(inst)
	h.insertMode = true
	h.insertModeSessionID = inst.ID
	h.previewCache[inst.ID] = "OpenAI Codex\n\n› composer\nmodel · ? for shortcuts\n"

	got := tmux.StripANSI(h.renderPreviewPane(80, 4))
	for _, want := range []string{"OpenAI Codex", "› composer", "? for shortcuts"} {
		if !strings.Contains(got, want) {
			t.Fatalf("focused Codex preview omitted %q: %q", want, got)
		}
	}
	for _, unwanted := range []string{inst.Title + "  ", "📁 ", "─ Output ─"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("focused Codex preview retained Agent Deck metadata %q: %q", unwanted, got)
		}
	}
}

func TestFocusedCodexTerminalDimensionsMatchRenderedPane(t *testing.T) {
	h := NewHome()
	h.width = 120
	h.height = 40
	h.previewPct = 65
	h.previewOrientation = PreviewOrientationRight

	cols, rows, ok := h.focusedCodexTerminalDimensions()
	if !ok {
		t.Fatal("dual-column dashboard should expose a terminal region")
	}
	_, wantCols := h.splitPaneWidths()
	// 40 total - header(1) - filter(1) - help(2) - panel title(2).
	if cols != wantCols || rows != 34 {
		t.Fatalf("focused terminal geometry = %dx%d, want %dx34", cols, rows, wantCols)
	}

	h.width = layoutBreakpointSingle - 1
	if _, _, ok := h.focusedCodexTerminalDimensions(); ok {
		t.Fatal("single-column layout has no terminal region to publish")
	}
}

func TestFocusedCodexGeometryPublishesOnlyWhenChanged(t *testing.T) {
	inst := session.NewInstanceWithTool("codex-geometry", t.TempDir(), "codex")
	tmuxSession := tmux.NewSession("codex-geometry", t.TempDir())
	inst.SetTmuxSessionForTest(tmuxSession)
	h := homeWithSession(inst)
	h.insertMode = true
	h.insertModeSessionID = inst.ID

	if cmd := h.syncFocusedCodexGeometryCmd(); cmd == nil {
		t.Fatal("entering focused Codex output did not publish terminal geometry")
	}
	cols, rows, ok := h.focusedCodexTerminalDimensions()
	if !ok {
		t.Fatal("test dashboard should expose a terminal region")
	}
	if h.codexPreviewGeometrySession != tmuxSession.Name ||
		h.codexPreviewGeometryCols != cols || h.codexPreviewGeometryRows != rows {
		t.Fatalf("published geometry signature = %q %dx%d, want %q %dx%d",
			h.codexPreviewGeometrySession, h.codexPreviewGeometryCols,
			h.codexPreviewGeometryRows, tmuxSession.Name, cols, rows)
	}
	if cmd := h.syncFocusedCodexGeometryCmd(); cmd != nil {
		t.Fatal("unchanged geometry scheduled another tmux round trip")
	}

	h.width++
	if cmd := h.syncFocusedCodexGeometryCmd(); cmd == nil {
		t.Fatal("viewport resize did not republish terminal geometry")
	}
	h.insertMode = false
	if cmd := h.syncFocusedCodexGeometryCmd(); cmd != nil {
		t.Fatal("leaving Codex output scheduled a geometry update")
	}
	if h.codexPreviewGeometrySession != "" || h.codexPreviewGeometryCols != 0 || h.codexPreviewGeometryRows != 0 {
		t.Fatal("leaving Codex output did not clear the geometry signature")
	}
}
