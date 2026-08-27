package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func TestInspectorRendersManagerPreviewAboveLiveOutput(t *testing.T) {
	model := newInspectorModel("default", "session-a")
	model.width = 86
	model.height = 24
	model.snapshot = inspectorSnapshot{
		instance: &session.Instance{
			ID:               "session-a",
			Title:            "career-ops",
			ProjectPath:      "/Users/test/code/career-ops",
			GroupPath:        "code",
			Tool:             "claude",
			Status:           session.StatusWaiting,
			ClaudeSessionID:  "3d2c2e4e-6c0b-4f7a-907a-4d90bdefc01e",
			ClaudeDetectedAt: time.Now(),
			LoadedMCPNames:   []string{"github", "linear"},
			CreatedAt:        time.Now().Add(-5 * time.Minute),
		},
		analytics: &session.SessionAnalytics{
			CurrentContextTokens: 40_000,
			InputTokens:          10_000,
			OutputTokens:         2_000,
			Model:                "claude-sonnet-4-20250514",
		},
		mcpNames: []string{"github", "linear"},
		canFork:  true,
	}

	plain := ansi.Strip(model.View())
	for _, want := range []string{
		"PREVIEW", "career-ops", "◐ waiting", "/Users/test/code/career-ops",
		"claude", "code", "Claude", "● Connected", "Session:", "Model:",
		"MCPs:", "Fork:", "Analytics", "Session Analytics", "Output",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("inspector missing %q:\n%s", want, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	if !strings.Contains(lines[len(lines)-1], "Output") {
		t.Fatalf("inspector must hand off to the live pane with Output divider on its last row:\n%s", plain)
	}
}

func TestInspectorKeepsUnsupportedToolAnalyticsHonest(t *testing.T) {
	model := newInspectorModel("default", "session-a")
	model.width = 72
	model.height = 18
	model.snapshot.instance = &session.Instance{
		ID:          "session-a",
		Title:       "agent-deck",
		ProjectPath: "/work/agent-deck",
		GroupPath:   "code",
		Tool:        "codex",
		Status:      session.StatusRunning,
		CreatedAt:   time.Now(),
	}
	plain := ansi.Strip(model.View())
	if !strings.Contains(plain, "Analytics unavailable for Codex") {
		t.Fatalf("unsupported analytics were hidden or invented:\n%s", plain)
	}
}

func TestInspectorStopsLoadingAfterEmptyAnalyticsAttempt(t *testing.T) {
	model := newInspectorModel("default", "session-a")
	model.width = 72
	model.height = 18
	model.snapshot = inspectorSnapshot{
		instance: &session.Instance{
			ID:          "session-a",
			Title:       "career-ops",
			ProjectPath: "/work/career-ops",
			Tool:        "claude",
			Status:      session.StatusWaiting,
			CreatedAt:   time.Now(),
		},
		analyticsAttempted: true,
	}
	plain := ansi.Strip(model.View())
	if !strings.Contains(plain, "No analytics data yet") || strings.Contains(plain, "Loading analytics") {
		t.Fatalf("empty analytics attempt rendered misleading state:\n%s", plain)
	}
}

func TestClampInspectorHeightPreservesInteractiveViewer(t *testing.T) {
	if got := clampInspectorHeight(18, 80); got != 18 {
		t.Fatalf("roomy inspector height = %d, want 18", got)
	}
	if got := clampInspectorHeight(18, 28); got != 13 {
		t.Fatalf("compact inspector height = %d, want 13", got)
	}
}
