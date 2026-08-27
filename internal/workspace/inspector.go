package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/ui"
)

const (
	inspectorRefreshInterval = 2 * time.Second
	inspectorAnalyticsTTL    = 5 * time.Second
)

// inspectorSnapshot is intentionally detached from the live Instance. The
// inspector is a read-only process and View must never perform filesystem or
// tmux I/O.
type inspectorSnapshot struct {
	instance           *session.Instance
	analytics          *session.SessionAnalytics
	geminiAnalytics    *session.GeminiSessionAnalytics
	analyticsAttempted bool
	analyticsErr       error
	mcpNames           []string
	canFork            bool
	loadedAt           time.Time
	err                error
}

type inspectorModel struct {
	profile          string
	instanceID       string
	width            int
	height           int
	snapshot         inspectorSnapshot
	analyticsFetched time.Time
	analyticsPanel   *ui.AnalyticsPanel
	analyticsConfig  session.AnalyticsDisplaySettings
}

type inspectorTickMsg struct{}
type inspectorLoadedMsg struct{ snapshot inspectorSnapshot }

// RunInspector starts the manager-style metadata and analytics pane above the
// selected agent's native terminal.
func RunInspector(ctx context.Context, profile, instanceID string) error {
	resolved, err := session.ResolveProfileForStorage(profile)
	if err != nil {
		return fmt.Errorf("resolve inspector profile: %w", err)
	}
	ui.InitTheme(session.GetTheme())
	model := newInspectorModel(resolved, instanceID)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = program.Run()
	return err
}

func newInspectorModel(profile, instanceID string) *inspectorModel {
	m := &inspectorModel{
		profile:        profile,
		instanceID:     strings.TrimSpace(instanceID),
		width:          80,
		height:         defaultInspectorHeight,
		analyticsPanel: ui.NewAnalyticsPanel(),
	}
	if cfg, err := session.LoadUserConfig(); err == nil && cfg != nil {
		m.analyticsConfig = cfg.Preview.GetAnalyticsSettings()
	}
	return m
}

func (m *inspectorModel) Init() tea.Cmd {
	return tea.Batch(m.loadCmd(true), inspectorTickCmd())
}

func (m *inspectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case inspectorTickMsg:
		withAnalytics := m.analyticsFetched.IsZero() || time.Since(m.analyticsFetched) >= inspectorAnalyticsTTL
		return m, tea.Batch(m.loadCmd(withAnalytics), inspectorTickCmd())
	case inspectorLoadedMsg:
		// A metadata refresh that deliberately skipped analytics carries the
		// last parsed values forward instead of flashing "Loading" every 2s.
		if !msg.snapshot.analyticsAttempted && !m.analyticsFetched.IsZero() {
			msg.snapshot.analytics = m.snapshot.analytics
			msg.snapshot.geminiAnalytics = m.snapshot.geminiAnalytics
			msg.snapshot.analyticsAttempted = m.snapshot.analyticsAttempted
			msg.snapshot.analyticsErr = m.snapshot.analyticsErr
		}
		if msg.snapshot.analyticsAttempted {
			m.analyticsFetched = time.Now()
		}
		m.snapshot = msg.snapshot
	}
	return m, nil
}

func (m *inspectorModel) loadCmd(withAnalytics bool) tea.Cmd {
	profile, instanceID := m.profile, m.instanceID
	return func() tea.Msg {
		return inspectorLoadedMsg{snapshot: loadInspectorSnapshot(profile, instanceID, withAnalytics)}
	}
}

func inspectorTickCmd() tea.Cmd {
	return tea.Tick(inspectorRefreshInterval, func(time.Time) tea.Msg { return inspectorTickMsg{} })
}

func loadInspectorSnapshot(profile, instanceID string, withAnalytics bool) inspectorSnapshot {
	snapshot := inspectorSnapshot{loadedAt: time.Now()}
	if strings.TrimSpace(instanceID) == "" {
		return snapshot
	}
	storage, err := session.NewLiveReadOnlyStorageWithProfile(profile)
	if err != nil {
		snapshot.err = err
		return snapshot
	}
	defer storage.Close()
	instances, err := storage.Load()
	if err != nil {
		snapshot.err = err
		return snapshot
	}
	for _, inst := range instances {
		if inst != nil && inst.ID == instanceID {
			snapshot.instance = inst
			break
		}
	}
	if snapshot.instance == nil {
		snapshot.err = fmt.Errorf("session no longer exists")
		return snapshot
	}

	snapshot.mcpNames = inspectorMCPNames(snapshot.instance)
	snapshot.canFork = snapshot.instance.CanFork()
	if !withAnalytics {
		return snapshot
	}
	snapshot.analyticsAttempted = true

	inst := snapshot.instance
	switch {
	case session.IsClaudeCompatible(inst.Tool):
		if path := inst.GetJSONLPath(); path != "" {
			snapshot.analytics, snapshot.analyticsErr = session.ParseSessionJSONL(path)
		}
	case inst.Tool == "gemini":
		analytics := &session.GeminiSessionAnalytics{}
		if inst.GeminiSessionID != "" {
			snapshot.analyticsErr = session.UpdateGeminiAnalyticsFromDisk(inst.ProjectPath, inst.GeminiSessionID, analytics)
			if snapshot.analyticsErr == nil {
				snapshot.geminiAnalytics = analytics
			}
		}
	}
	return snapshot
}

func inspectorMCPNames(inst *session.Instance) []string {
	if inst == nil {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range inst.LoadedMCPNames {
		add(name)
	}
	if info := inst.GetMCPInfo(); info != nil {
		for _, name := range info.Global {
			add(name)
		}
		for _, name := range info.Project {
			add(name)
		}
		for _, local := range info.LocalMCPs {
			add(local.Name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *inspectorModel) View() string {
	width := maxInt(m.width, 20)
	height := maxInt(m.height, minimumInspectorHeight)
	lines := []string{
		panelTitle("PREVIEW", width),
		lipgloss.NewStyle().Foreground(ui.ColorBorder).Render(strings.Repeat("─", width)),
	}

	if m.snapshot.err != nil && m.snapshot.instance == nil {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(ui.ColorRed).Render("Unable to load preview"),
			lipgloss.NewStyle().Foreground(ui.ColorTextDim).Render(truncateRunes(m.snapshot.err.Error(), width)),
		)
		return inspectorFrame(lines, width, height)
	}
	inst := m.snapshot.instance
	if inst == nil {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(ui.ColorTextDim).Italic(true).Render("Select a session to preview"),
		)
		return inspectorFrame(lines, width, height)
	}

	status := inst.GetStatusThreadSafe()
	statusText, statusColor := inspectorStatus(status, inst.IsArchived())
	name := lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true).Render(truncateRunes(inst.Title, maxInt(8, width-18)))
	badge := lipgloss.NewStyle().Foreground(statusColor).Render(statusText)
	lines = append(lines, name+"  "+badge)

	path := truncateMiddle(inst.ProjectPath, maxInt(8, width-3))
	lines = append(lines, lipgloss.NewStyle().Foreground(ui.ColorText).Render("▰ "+path))
	lines = append(lines, lipgloss.NewStyle().Foreground(ui.ColorTextDim).Render("◷ "+inspectorActivity(inst, status)))

	toolBadge := lipgloss.NewStyle().Foreground(ui.ColorBg).Background(ui.ToolColor(inst.Tool)).Padding(0, 1).Render(inst.Tool)
	group := strings.TrimSpace(inst.GroupPath)
	if group == "" {
		group = session.DefaultGroupPath
	}
	groupBadge := lipgloss.NewStyle().Foreground(ui.ColorBg).Background(ui.ColorCyan).Padding(0, 1).Render(group)
	lines = append(lines, toolBadge+" "+groupBadge)

	lines = append(lines, inspectorDivider(inspectorToolName(inst.Tool), width))
	lines = append(lines, inspectorToolLines(inst, m.snapshot.mcpNames, m.snapshot.canFork, width)...)
	lines = append(lines, inspectorDivider("Analytics", width))
	lines = append(lines, m.analyticsLines(inst, width)...)

	return inspectorFrame(lines, width, height)
}

func (m *inspectorModel) analyticsLines(inst *session.Instance, width int) []string {
	m.analyticsPanel.SetDisplaySettings(m.analyticsConfig)
	m.analyticsPanel.SetSize(maxInt(20, width-2), maxInt(4, m.height/2))
	switch {
	case m.snapshot.analytics != nil:
		m.analyticsPanel.SetAnalytics(m.snapshot.analytics)
	case m.snapshot.geminiAnalytics != nil:
		m.analyticsPanel.SetGeminiAnalytics(m.snapshot.geminiAnalytics)
	default:
		if session.IsClaudeCompatible(inst.Tool) || inst.Tool == "gemini" {
			if !m.snapshot.analyticsAttempted {
				return []string{lipgloss.NewStyle().Foreground(ui.ColorText).Italic(true).Render("Loading analytics...")}
			}
			if m.snapshot.analyticsErr != nil {
				return []string{lipgloss.NewStyle().Foreground(ui.ColorTextDim).Italic(true).Render("Analytics unavailable: " + truncateRunes(m.snapshot.analyticsErr.Error(), maxInt(8, width-23)))}
			}
			return []string{lipgloss.NewStyle().Foreground(ui.ColorTextDim).Italic(true).Render("No analytics data yet")}
		}
		return []string{lipgloss.NewStyle().Foreground(ui.ColorTextDim).Italic(true).Render("Analytics unavailable for " + inspectorToolName(inst.Tool))}
	}
	view := strings.TrimSuffix(m.analyticsPanel.View(), "\n")
	return strings.Split(view, "\n")
}

func inspectorToolLines(inst *session.Instance, mcpNames []string, canFork bool, width int) []string {
	label := lipgloss.NewStyle().Foreground(ui.ColorText)
	value := lipgloss.NewStyle().Foreground(ui.ColorText)
	statusText, statusColor := inspectorConnection(inst)
	lines := []string{label.Render("Status:  ") + lipgloss.NewStyle().Foreground(statusColor).Render(statusText)}
	if id := inspectorSessionID(inst); id != "" {
		lines = append(lines, label.Render("Session: ")+value.Render(truncateRunes(id, maxInt(8, width-9))))
	}
	lines = append(lines, label.Render("Model:   ")+value.Render(inspectorModelLabel(inst)))
	if len(mcpNames) > 0 {
		lines = append(lines, label.Render("MCPs:    ")+value.Render(truncateRunes(strings.Join(mcpNames, ", "), maxInt(8, width-9))))
	}
	if len(inst.Plugins) > 0 {
		lines = append(lines, label.Render("Plugins: ")+value.Render(truncateRunes(strings.Join(inst.Plugins, ", "), maxInt(8, width-9))))
	}
	if len(inst.Channels) > 0 {
		lines = append(lines, label.Render("Channels:")+value.Render(" "+truncateRunes(strings.Join(inst.Channels, ", "), maxInt(8, width-10))))
	}
	if canFork {
		key := lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
		hint := lipgloss.NewStyle().Foreground(ui.ColorTextDim).Italic(true)
		lines = append(lines, hint.Render("Fork:    ")+key.Render("f")+hint.Render(" quick fork, ")+key.Render("F")+hint.Render(" fork with options"))
	}
	return lines
}

func inspectorFrame(lines []string, width, height int) string {
	// The horizontal pane below is the real terminal, so the inspector always
	// ends with the same Output divider used by the full preview pane.
	bodyHeight := maxInt(1, height-1)
	if len(lines) > bodyHeight {
		lines = lines[:bodyHeight]
	}
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	lines = append(lines, inspectorDivider("Output", width))
	for index, line := range lines {
		lines[index] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func panelTitle(title string, width int) string {
	return fitLine(lipgloss.NewStyle().Foreground(ui.ColorCyan).Bold(true).Render(title), width)
}

func inspectorDivider(label string, width int) string {
	labelWidth := lipgloss.Width(label) + 2
	left := maxInt(2, (width-labelWidth)/2)
	right := maxInt(2, width-labelWidth-left)
	line := lipgloss.NewStyle().Foreground(ui.ColorBorder)
	title := lipgloss.NewStyle().Foreground(ui.ColorText).Bold(true)
	return line.Render(strings.Repeat("─", left)) + " " + title.Render(label) + " " + line.Render(strings.Repeat("─", right))
}

func inspectorStatus(status session.Status, archived bool) (string, lipgloss.Color) {
	if archived {
		return "◌ archived", ui.ColorTextDim
	}
	switch status {
	case session.StatusRunning:
		return "● running", ui.ColorGreen
	case session.StatusWaiting:
		return "◐ waiting", ui.ColorYellow
	case session.StatusError:
		return "✕ error", ui.ColorRed
	case session.StatusStopped:
		return "■ stopped", ui.ColorTextDim
	case session.StatusStarting, session.StatusQueued:
		return "◐ " + string(status), ui.ColorYellow
	default:
		return "○ " + string(status), ui.ColorTextDim
	}
}

func inspectorConnection(inst *session.Instance) (string, lipgloss.Color) {
	if inst.IsArchived() {
		return "◌ Archived", ui.ColorTextDim
	}
	if inspectorSessionID(inst) == "" {
		return "○ Not connected", ui.ColorTextDim
	}
	status := inst.GetStatusThreadSafe()
	if status == session.StatusStopped || status == session.StatusError {
		return "○ Disconnected", ui.ColorTextDim
	}
	return "● Connected", ui.ColorGreen
}

func inspectorSessionID(inst *session.Instance) string {
	switch {
	case session.IsClaudeCompatible(inst.Tool):
		return inst.ClaudeSessionID
	case inst.Tool == "gemini":
		return inst.GeminiSessionID
	case inst.Tool == "opencode":
		return inst.OpenCodeSessionID
	case inst.Tool == "codex":
		return inst.CodexSessionID
	default:
		// The inspector is read-only; GetGenericSessionID may consult the live
		// pane and persist a newly discovered binding. Show only stored data here.
		return strings.TrimSpace(inst.GenericSessionID)
	}
}

func inspectorModelLabel(inst *session.Instance) string {
	modelID := ""
	switch {
	case session.IsClaudeCompatible(inst.Tool):
		if opts, err := session.UnmarshalClaudeOptions(inst.ToolOptionsJSON); err == nil && opts != nil {
			modelID = opts.Model
		}
	case session.IsCodexCompatible(inst.Tool):
		if opts, err := session.UnmarshalCodexOptions(inst.ToolOptionsJSON); err == nil && opts != nil {
			modelID = opts.Model
		}
	case inst.Tool == "gemini":
		modelID = inst.GeminiModel
	}
	if display := session.ParseModelID(modelID).Display(); display != "" {
		return display
	}
	return "tool default"
}

func inspectorToolName(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return "Shell"
	}
	runes := []rune(tool)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

func inspectorActivity(inst *session.Instance, status session.Status) string {
	if status == session.StatusRunning {
		return "active now"
	}
	activity := inst.LastAccessedAt
	if inst.LastStartedAt.After(activity) {
		activity = inst.LastStartedAt
	}
	if activity.IsZero() {
		activity = inst.CreatedAt
	}
	if activity.IsZero() {
		return "activity unknown"
	}
	d := time.Since(activity)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func truncateMiddle(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width < 5 {
		return truncateRunes(value, width)
	}
	runes := []rune(value)
	left := (width - 1) / 2
	right := width - left - 1
	if left+right >= len(runes) {
		return value
	}
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
}
