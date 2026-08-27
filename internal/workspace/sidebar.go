package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

const (
	sessionRefreshInterval = time.Second
	switchDebounce         = 120 * time.Millisecond
)

type paneController interface {
	ShowInstance(context.Context, string) error
	ActivateInstance(context.Context, string) error
	FocusAgent(context.Context) error
	Detach(context.Context) error
	ManagerCommand() *exec.Cmd
	ClassicAttachCommand(string) *exec.Cmd
	SetZoom(context.Context, bool) error
}

type sidebarItem struct {
	data      *session.InstanceData
	attachKey string
}

type sidebarModel struct {
	profile    string
	controller paneController
	items      []sidebarItem
	cursor     int
	offset     int
	width      int
	height     int
	activeID   string
	activeKey  string
	generation uint64
	err        error
	busy       string
}

type refreshTickMsg struct{}

type sessionsLoadedMsg struct {
	items []sidebarItem
	err   error
}

type switchRequestMsg struct {
	generation uint64
	instanceID string
	attachKey  string
}

type switchFinishedMsg struct {
	generation uint64
	instanceID string
	attachKey  string
	err        error
}

type focusFinishedMsg struct {
	instanceID string
	attachKey  string
	err        error
}

type managerFinishedMsg struct{ err error }
type zoomRestoredMsg struct{ err error }
type detachFinishedMsg struct{ err error }

// RunSidebar starts the compact navigator inside the private outer tmux pane.
func RunSidebar(ctx context.Context, opts SidebarOptions) error {
	// The navigator itself runs inside the OUTER compositor. Scrub its tmux
	// identity before loading Agent Deck state so legacy/default-socket sessions
	// continue to resolve against the user's actual default server.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	profile, err := session.ResolveProfileForStorage(opts.Profile)
	if err != nil {
		return fmt.Errorf("resolve sidebar profile: %w", err)
	}
	binaryPath, err := resolveBinaryPath(opts.BinaryPath)
	if err != nil {
		return err
	}
	opts.Profile = profile
	opts.BinaryPath = binaryPath
	controller, err := NewController(opts)
	if err != nil {
		return err
	}
	items, loadErr := loadSidebarItems(profile)
	model := newSidebarModel(profile, controller, items)
	model.err = loadErr
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = program.Run()
	return err
}

func newSidebarModel(profile string, controller paneController, items []sidebarItem) *sidebarModel {
	return &sidebarModel{
		profile:    profile,
		controller: controller,
		items:      items,
		width:      DefaultSidebarWidth,
		height:     30,
	}
}

func (m *sidebarModel) Init() tea.Cmd {
	return tea.Batch(refreshTickCmd(), m.scheduleSwitch())
}

func (m *sidebarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureCursorVisible()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if len(m.items) > 0 && m.cursor > 0 {
				m.cursor--
				m.err = nil
				m.ensureCursorVisible()
				return m, m.scheduleSwitch()
			}
		case "down", "j":
			if len(m.items) > 0 && m.cursor < len(m.items)-1 {
				m.cursor++
				m.err = nil
				m.ensureCursorVisible()
				return m, m.scheduleSwitch()
			}
		case "g", "home":
			if len(m.items) > 0 && m.cursor != 0 {
				m.cursor = 0
				m.ensureCursorVisible()
				return m, m.scheduleSwitch()
			}
		case "G", "end":
			if len(m.items) > 0 && m.cursor != len(m.items)-1 {
				m.cursor = len(m.items) - 1
				m.ensureCursorVisible()
				return m, m.scheduleSwitch()
			}
		case "enter":
			item := m.selected()
			if item == nil {
				return m, nil
			}
			m.busy = "opening " + item.data.Title
			return m, m.focusSelectedCmd(*item)
		case "m", "n":
			return m, m.execZoomed(m.controller.ManagerCommand(), "manager")
		case "q", "ctrl+c":
			m.busy = "detaching"
			return m, func() tea.Msg {
				return detachFinishedMsg{err: m.controller.Detach(context.Background())}
			}
		}

	case refreshTickMsg:
		return m, tea.Batch(loadSessionsCmd(m.profile), refreshTickCmd())

	case sessionsLoadedMsg:
		m.applyItems(msg.items)
		if msg.err != nil {
			m.err = msg.err
		}
		item := m.selected()
		if item != nil && item.attachKey != m.activeKey {
			return m, m.scheduleSwitch()
		}
		if item == nil && m.activeID != "" {
			return m, m.scheduleSwitch()
		}

	case switchRequestMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		return m, func() tea.Msg {
			err := m.controller.ShowInstance(context.Background(), msg.instanceID)
			return switchFinishedMsg{
				generation: msg.generation,
				instanceID: msg.instanceID,
				attachKey:  msg.attachKey,
				err:        err,
			}
		}

	case switchFinishedMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.activeID = msg.instanceID
		m.activeKey = msg.attachKey
		m.err = nil

	case focusFinishedMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.activeID = msg.instanceID
		m.activeKey = msg.attachKey
		m.err = nil

	case managerFinishedMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err
		}
		return m, restoreZoomCmd(m.controller)

	case zoomRestoredMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		return m, loadSessionsCmd(m.profile)

	case detachFinishedMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err
		}
	}

	return m, nil
}

func (m *sidebarModel) View() string {
	width := m.width
	if width <= 0 {
		width = DefaultSidebarWidth
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("236")).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	lines := selectedSummaryLines(m.selected(), width, dimStyle, activeStyle)
	lines = append(lines,
		fitLine(dimStyle.Render(" "+m.profile+" · workspace"), width),
		strings.Repeat("─", maxInt(1, width)),
	)

	contentLimit := maxInt(1, height-len(lines)-2)
	contentLines := make([]string, 0, contentLimit)
	lastGroup := "\x00"
	for index := m.offset; index < len(m.items) && len(contentLines) < contentLimit; index++ {
		item := m.items[index]
		group := strings.TrimSpace(item.data.GroupPath)
		if group == "" {
			group = "Ungrouped"
		}
		if group != lastGroup && len(contentLines) < contentLimit {
			contentLines = append(contentLines, fitLine(dimStyle.Render(" "+group), width))
			lastGroup = group
		}
		if len(contentLines) >= contentLimit {
			break
		}

		cursor := " "
		if index == m.cursor {
			cursor = "›"
		}
		active := " "
		if item.data.ID == m.activeID {
			active = activeStyle.Render("●")
		}
		status := statusGlyph(item.data)
		titleWidth := maxInt(4, width-7)
		line := fmt.Sprintf("%s%s%s %s", cursor, active, status, truncateRunes(item.data.Title, titleWidth))
		line = fitLine(line, width)
		if index == m.cursor {
			line = selectedStyle.Width(width).Render(line)
		}
		contentLines = append(contentLines, line)
		if len(contentLines) >= contentLimit {
			break
		}
		detail := itemDetail(item.data)
		contentLines = append(contentLines, fitLine(dimStyle.Render("    "+detail), width))
	}
	if len(m.items) == 0 {
		contentLines = append(contentLines, " No managed sessions", "", " Press m to open manager")
	}
	for len(contentLines) < contentLimit {
		contentLines = append(contentLines, "")
	}
	lines = append(lines, contentLines[:contentLimit]...)

	status := "↑↓ switch  enter focus"
	if m.busy != "" {
		status = m.busy
	} else if m.err != nil {
		status = errorStyle.Render(truncateRunes(m.err.Error(), maxInt(1, width)))
	}
	lines = append(lines,
		fitLine(status, width),
		fitLine(dimStyle.Render("m manage  q detach"), width),
	)
	return strings.Join(lines, "\n")
}

func selectedSummaryLines(item *sidebarItem, width int, dimStyle, activeStyle lipgloss.Style) []string {
	if item == nil || item.data == nil {
		return []string{
			fitLine(" No session selected", width),
			fitLine(dimStyle.Render(" Status: —"), width),
			fitLine(dimStyle.Render(" Model:  —"), width),
		}
	}

	inst := item.data
	tool := strings.ToUpper(strings.TrimSpace(inst.Tool))
	if tool == "" {
		tool = "SHELL"
	}
	titleWidth := maxInt(1, width-utf8.RuneCountInString(tool)-3)
	title := truncateRunes(inst.Title, titleWidth)
	gap := maxInt(1, width-utf8.RuneCountInString(title)-utf8.RuneCountInString(tool)-2)
	titleLine := " " + title + strings.Repeat(" ", gap) + activeStyle.Render(tool)

	status := strings.TrimSpace(string(inst.Status))
	if status == "" {
		status = "unknown"
	}
	model := selectedModelLabel(inst)
	if model == "" {
		model = "tool default"
	}

	return []string{
		fitLine(titleLine, width),
		fitLine(" Status: "+statusGlyph(inst)+" "+status, width),
		fitLine(dimStyle.Render(" Model:  "+model), width),
	}
}

func selectedModelLabel(inst *session.InstanceData) string {
	if inst == nil {
		return ""
	}
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
	return session.ParseModelID(modelID).Display()
}

func (m *sidebarModel) selected() *sidebarItem {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

func (m *sidebarModel) scheduleSwitch() tea.Cmd {
	m.generation++
	generation := m.generation
	item := m.selected()
	instanceID := ""
	attachKey := ""
	if item != nil {
		instanceID = item.data.ID
		attachKey = item.attachKey
	}
	return tea.Tick(switchDebounce, func(time.Time) tea.Msg {
		return switchRequestMsg{generation: generation, instanceID: instanceID, attachKey: attachKey}
	})
}

func (m *sidebarModel) focusSelectedCmd(item sidebarItem) tea.Cmd {
	generation := m.generation + 1
	m.generation = generation
	return func() tea.Msg {
		err := m.controller.ActivateInstance(context.Background(), item.data.ID)
		return focusFinishedMsg{instanceID: item.data.ID, attachKey: item.attachKey, err: err}
	}
}

func (m *sidebarModel) execZoomed(command *exec.Cmd, label string) tea.Cmd {
	if err := m.controller.SetZoom(context.Background(), true); err != nil {
		m.err = err
		return nil
	}
	m.busy = label
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return managerFinishedMsg{err: err}
	})
}

func restoreZoomCmd(controller paneController) tea.Cmd {
	return func() tea.Msg {
		return zoomRestoredMsg{err: controller.SetZoom(context.Background(), false)}
	}
}

func refreshTickCmd() tea.Cmd {
	return tea.Tick(sessionRefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func loadSessionsCmd(profile string) tea.Cmd {
	return func() tea.Msg {
		items, err := loadSidebarItems(profile)
		return sessionsLoadedMsg{items: items, err: err}
	}
}

func loadSidebarItems(profile string) ([]sidebarItem, error) {
	storage, err := session.NewLiveReadOnlyStorageWithProfile(profile)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	instances, _, err := storage.LoadLite()
	if err != nil {
		return nil, err
	}
	items := make([]sidebarItem, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		items = append(items, sidebarItem{data: inst, attachKey: attachKey(inst)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].data, items[j].data
		if left.ArchivedAt.IsZero() != right.ArchivedAt.IsZero() {
			return left.ArchivedAt.IsZero()
		}
		if left.GroupPath != right.GroupPath {
			return left.GroupPath < right.GroupPath
		}
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return strings.ToLower(left.Title) < strings.ToLower(right.Title)
	})
	return items, nil
}

func (m *sidebarModel) applyItems(items []sidebarItem) {
	selectedID := ""
	if selected := m.selected(); selected != nil {
		selectedID = selected.data.ID
	}
	m.items = items
	if len(items) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if selectedID != "" {
		for index := range items {
			if items[index].data.ID == selectedID {
				m.cursor = index
				m.ensureCursorVisible()
				return
			}
		}
	}
	if m.cursor >= len(items) {
		m.cursor = len(items) - 1
	}
	m.ensureCursorVisible()
}

func (m *sidebarModel) ensureCursorVisible() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Six fixed header lines and two footer lines surround two lines per item.
	visibleItems := maxInt(1, (m.height-8)/2)
	if m.cursor >= m.offset+visibleItems {
		m.offset = m.cursor - visibleItems + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func attachKey(inst *session.InstanceData) string {
	if inst == nil {
		return ""
	}
	return strings.Join([]string{
		inst.ID,
		inst.TmuxSocketName,
		inst.TmuxSession,
		inst.SSHHost,
		inst.ArchivedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
}

func statusGlyph(inst *session.InstanceData) string {
	if inst == nil {
		return "?"
	}
	if !inst.ArchivedAt.IsZero() {
		return "◌"
	}
	switch inst.Status {
	case session.StatusRunning:
		return "●"
	case session.StatusWaiting:
		return "!"
	case session.StatusStarting, session.StatusQueued:
		return "◐"
	case session.StatusIdle:
		return "○"
	case session.StatusError:
		return "×"
	case session.StatusStopped:
		return "■"
	default:
		return "·"
	}
}

func itemDetail(inst *session.InstanceData) string {
	if inst == nil {
		return ""
	}
	tool := strings.TrimSpace(inst.Tool)
	if tool == "" {
		tool = "shell"
	}
	location := ""
	if inst.SSHHost != "" {
		location = inst.SSHHost
	} else if inst.ProjectPath != "" {
		location = filepath.Base(filepath.Clean(inst.ProjectPath))
	}
	if location == "." || location == string(os.PathSeparator) {
		location = inst.ProjectPath
	}
	if location == "" {
		return tool + " · " + string(inst.Status)
	}
	return tool + " · " + location
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	plainWidth := lipgloss.Width(value)
	if plainWidth > width {
		return truncateRunes(value, width)
	}
	return value + strings.Repeat(" ", width-plainWidth)
}

func truncateRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
