package ui

import (
	"strings"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestQueueTerminalOutputOnlyTargetsFocusedAgent(t *testing.T) {
	h := &Home{}
	var messages []tea.Msg
	h.SetMessageSender(func(msg tea.Msg) { messages = append(messages, msg) })
	h.activeTerminalTmux.Store("focused")

	h.queueTerminalOutput("background")
	if len(messages) != 0 {
		t.Fatalf("background output queued %d UI messages", len(messages))
	}
	h.queueTerminalOutput("focused")
	if len(messages) != 1 {
		t.Fatalf("focused output queued %d UI messages, want 1", len(messages))
	}
	h.queueTerminalOutput("focused")
	if len(messages) != 1 {
		t.Fatalf("coalescing failed: queued %d UI messages", len(messages))
	}
}

func TestOutputLoadoutSummaryKeepsClaudeAndCursorSeparate(t *testing.T) {
	claude := &session.Instance{ID: "claude-id", Tool: "claude"}
	cursor := &session.Instance{ID: "cursor-id", Tool: "cursor"}
	h := &Home{capabilitiesCache: map[string]session.LocalAgentCapabilities{
		claude.ID: {Tools: []string{"claude-mcp"}, Skills: []string{"claude-skill"}},
		cursor.ID: {Tools: []string{"cursor-mcp"}, Skills: []string{"cursor-skill"}},
	}}

	claudeSummary := h.outputLoadoutSummary(claude)
	if !strings.Contains(claudeSummary, "Claude tools: claude-mcp") ||
		!strings.Contains(claudeSummary, "skills: claude-skill") ||
		strings.Contains(claudeSummary, "cursor-") {
		t.Fatalf("Claude summary mixed provider loadouts: %q", claudeSummary)
	}
	cursorSummary := h.outputLoadoutSummary(cursor)
	if !strings.Contains(cursorSummary, "Cursor tools: cursor-mcp") ||
		!strings.Contains(cursorSummary, "skills: cursor-skill") ||
		strings.Contains(cursorSummary, "claude-") {
		t.Fatalf("Cursor summary mixed provider loadouts: %q", cursorSummary)
	}
}

func TestOutputLoadoutSummaryIncludesCodexContext(t *testing.T) {
	codex := &session.Instance{ID: "codex-id", Tool: "codex"}
	h := &Home{
		capabilitiesCache: map[string]session.LocalAgentCapabilities{
			codex.ID: {Tools: []string{"docs"}, Skills: []string{"review"}},
		},
		codexAnalyticsCache: map[string]*session.CodexSessionAnalytics{
			codex.ID: {CurrentContextTokens: 62_000, ContextWindow: 212_000},
		},
	}

	summary := h.outputLoadoutSummary(codex)
	for _, want := range []string{"Context [", "25%", "Codex tools: docs", "skills: review"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("Codex summary missing %q: %q", want, summary)
		}
	}
}
