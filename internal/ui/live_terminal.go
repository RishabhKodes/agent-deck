package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

const (
	terminalCaretBlinkInterval = 500 * time.Millisecond
	capabilitiesCacheTTL       = 15 * time.Second
)

// SetMessageSender connects background tmux output notifications to Bubble
// Tea's event loop. It is called by main immediately after Program creation.
func (h *Home) SetMessageSender(sender func(tea.Msg)) {
	h.messageSenderMu.Lock()
	h.messageSender = sender
	h.messageSenderMu.Unlock()
}

func (h *Home) queueTerminalOutput(sessionName string) {
	if strings.TrimSpace(sessionName) == "" {
		return
	}
	active, _ := h.activeTerminalTmux.Load().(string)
	if active == "" || active != sessionName {
		return
	}
	if _, alreadyQueued := h.terminalOutputQueued.LoadOrStore(sessionName, struct{}{}); alreadyQueued {
		return
	}
	h.messageSenderMu.RLock()
	sender := h.messageSender
	h.messageSenderMu.RUnlock()
	if sender == nil {
		h.terminalOutputQueued.Delete(sessionName)
		return
	}
	sender(terminalOutputMsg{sessionName: sessionName})
}

func (h *Home) requestActiveTerminalFrame() tea.Cmd {
	if !h.insertMode {
		return nil
	}
	inst, key, windowIndex := h.selectedPreviewTarget()
	if !h.isFocusedAgentTerminal(inst) || key == "" {
		return nil
	}
	if h.activeTerminalFrameFetching {
		h.activeTerminalFrameDirty = true
		return nil
	}
	h.activeTerminalFrameFetching = true
	h.activeTerminalFrameDirty = false
	generation := h.caretBlinkGeneration
	return func() tea.Msg {
		frame, err := inst.PreviewFrame(windowIndex)
		return activeTerminalFrameFetchedMsg{previewKey: key, generation: generation, frame: frame, err: err}
	}
}

func (h *Home) scheduleCaretBlink() tea.Cmd {
	generation := h.caretBlinkGeneration
	return tea.Tick(terminalCaretBlinkInterval, func(time.Time) tea.Msg {
		return caretBlinkMsg{generation: generation}
	})
}

// consumeOutputActivationCmd performs the side effects that must accompany
// entry into Output mode. A pending bit lets every activation path share this
// code even though enterInsertMode historically returns only a bool.
func (h *Home) consumeOutputActivationCmd() tea.Cmd {
	if !h.insertMode || !h.outputActivationPending {
		return nil
	}
	h.outputActivationPending = false
	cmds := []tea.Cmd{
		// Wheel events and Shift-drag selection both rely on mouse reports
		// reaching Bubble Tea.
		tea.EnableMouseCellMotion,
		h.fetchSelectedPreview(),
		h.requestActiveTerminalFrame(),
		h.scheduleCaretBlink(),
	}
	if inst, _, _ := h.selectedPreviewTarget(); inst != nil {
		if cmd := h.fetchLocalCapabilities(inst, false); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if session.IsCodexCompatible(inst.GetToolThreadSafe()) && h.analyticsFetchingID != inst.ID {
			h.analyticsFetchingID = inst.ID
			cmds = append(cmds, h.fetchAnalytics(inst))
		}
	}
	return tea.Batch(cmds...)
}

func supportsLocalCapabilities(tool string) bool {
	return session.IsClaudeCompatible(tool) || tool == "cursor" || session.IsCodexCompatible(tool)
}

func (h *Home) fetchLocalCapabilities(inst *session.Instance, force bool) tea.Cmd {
	if inst == nil || !supportsLocalCapabilities(inst.GetToolThreadSafe()) {
		return nil
	}
	h.capabilitiesMu.Lock()
	if h.capabilitiesCache == nil {
		h.capabilitiesCache = make(map[string]session.LocalAgentCapabilities)
		h.capabilitiesCacheTime = make(map[string]time.Time)
		h.capabilitiesFetching = make(map[string]bool)
	}
	if h.capabilitiesFetching[inst.ID] {
		h.capabilitiesMu.Unlock()
		return nil
	}
	if !force {
		if cachedAt, ok := h.capabilitiesCacheTime[inst.ID]; ok && time.Since(cachedAt) < capabilitiesCacheTTL {
			h.capabilitiesMu.Unlock()
			return nil
		}
	}
	h.capabilitiesFetching[inst.ID] = true
	h.capabilitiesMu.Unlock()

	sessionID := inst.ID
	return func() tea.Msg {
		return capabilitiesFetchedMsg{
			sessionID:    sessionID,
			capabilities: inst.GetLocalAgentCapabilities(),
		}
	}
}

func (h *Home) refreshCodexAnalyticsIfStale(inst *session.Instance) tea.Cmd {
	if inst == nil || !session.IsCodexCompatible(inst.GetToolThreadSafe()) || h.analyticsFetchingID == inst.ID {
		return nil
	}
	h.analyticsCacheMu.RLock()
	lastFetch, cached := h.analyticsCacheTime[inst.ID]
	h.analyticsCacheMu.RUnlock()
	if cached && time.Since(lastFetch) < analyticsCacheTTL {
		return nil
	}
	h.analyticsFetchingID = inst.ID
	return h.fetchAnalytics(inst)
}

func (h *Home) localCapabilities(sessionID string) (session.LocalAgentCapabilities, bool) {
	h.capabilitiesMu.RLock()
	capabilities, ok := h.capabilitiesCache[sessionID]
	h.capabilitiesMu.RUnlock()
	return capabilities, ok
}

func (h *Home) invalidateLocalCapabilities(sessionID string) {
	h.capabilitiesMu.Lock()
	delete(h.capabilitiesCache, sessionID)
	delete(h.capabilitiesCacheTime, sessionID)
	delete(h.capabilitiesFetching, sessionID)
	h.capabilitiesMu.Unlock()
}

func compactCapabilityNames(names []string, limit int) string {
	if len(names) == 0 {
		return "none"
	}
	if limit < 1 || len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:limit], ", ") + fmt.Sprintf(" +%d", len(names)-limit)
}

func (h *Home) renderLocalCapabilityLines(b *strings.Builder, inst *session.Instance, width int, includeTools bool) {
	if b == nil || inst == nil || !supportsLocalCapabilities(inst.GetToolThreadSafe()) {
		return
	}
	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	valueStyle := lipgloss.NewStyle().Foreground(ColorAccent)
	capabilities, ok := h.localCapabilities(inst.ID)
	if !ok {
		label := "Skills:  "
		if includeTools {
			label = "Tools:   "
		}
		b.WriteString(labelStyle.Render(label))
		b.WriteString(lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render("loading local config..."))
		b.WriteString("\n")
		return
	}
	maxValueWidth := max(8, width-13)
	skills := cellTruncate(compactCapabilityNames(capabilities.Skills, 0), maxValueWidth, "...")
	if includeTools {
		tools := cellTruncate(compactCapabilityNames(capabilities.Tools, 0), maxValueWidth, "...")
		b.WriteString(labelStyle.Render("Tools:   "))
		b.WriteString(valueStyle.Render(tools))
		b.WriteString("\n")
	}
	b.WriteString(labelStyle.Render("Skills:  "))
	b.WriteString(valueStyle.Render(skills))
	b.WriteString("\n")
}

func providerDisplayName(tool string) string {
	switch {
	case session.IsClaudeCompatible(tool):
		return "Claude"
	case session.IsCodexCompatible(tool):
		return "Codex"
	case tool == "cursor":
		return "Cursor"
	default:
		return tool
	}
}

func (h *Home) outputLoadoutSummary(inst *session.Instance) string {
	if inst == nil {
		return ""
	}
	var parts []string
	if session.IsCodexCompatible(inst.GetToolThreadSafe()) {
		h.analyticsCacheMu.RLock()
		analytics := h.codexAnalyticsCache[inst.ID]
		h.analyticsCacheMu.RUnlock()
		if analytics != nil && analytics.ContextWindow > 0 {
			percent := analytics.ContextPercent()
			filled := min(8, max(0, int(percent/12.5)))
			bar := strings.Repeat("█", filled) + strings.Repeat("░", 8-filled)
			parts = append(parts, fmt.Sprintf("Context [%s] %.0f%%", bar, percent))
		} else {
			parts = append(parts, "Context loading")
		}
	}
	if capabilities, ok := h.localCapabilities(inst.ID); ok {
		provider := providerDisplayName(inst.GetToolThreadSafe())
		parts = append(parts,
			fmt.Sprintf("%s tools: %s", provider, compactCapabilityNames(capabilities.Tools, 2)),
			fmt.Sprintf("skills: %s", compactCapabilityNames(capabilities.Skills, 2)),
		)
	} else if supportsLocalCapabilities(inst.GetToolThreadSafe()) {
		parts = append(parts, providerDisplayName(inst.GetToolThreadSafe())+" tools/skills loading")
	}
	return strings.Join(parts, " · ")
}

func (h *Home) renderCodexContextLine(b *strings.Builder, inst *session.Instance, width int) {
	if b == nil || inst == nil || !session.IsCodexCompatible(inst.GetToolThreadSafe()) {
		return
	}
	h.analyticsCacheMu.RLock()
	analytics := h.codexAnalyticsCache[inst.ID]
	h.analyticsCacheMu.RUnlock()
	if analytics == nil || analytics.ContextWindow <= 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorText).Bold(true).Render("Context "))
		b.WriteString(lipgloss.NewStyle().Foreground(ColorTextDim).Italic(true).Render("loading..."))
		b.WriteString("\n")
		return
	}
	b.WriteString(renderContextUsageBar(analytics.ContextPercent(), max(10, width-4)))
	b.WriteString("\n")
}
