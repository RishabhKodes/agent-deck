package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/sysinfo"
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
	ManagerCommand(string, string) *exec.Cmd
	ClassicAttachCommand(string) *exec.Cmd
	SetZoom(context.Context, bool) error
	UpdateChrome(context.Context, string, string) error
}

type sidebarItem struct {
	data      *session.InstanceData
	attachKey string
}

type sidebarModel struct {
	profile           string
	controller        paneController
	allItems          []sidebarItem
	items             []sidebarItem
	cursor            int
	offset            int
	width             int
	height            int
	activeID          string
	activeKey         string
	generation        uint64
	err               error
	busy              string
	filter            workspaceFilter
	activeExcludes    map[session.Status]bool
	systemStats       sysinfo.Stats
	systemStatsConfig session.SystemStatsSettings
	lastChrome        string
}

type workspaceFilter string

const (
	workspaceFilterAll      workspaceFilter = ""
	workspaceFilterRunning  workspaceFilter = "running"
	workspaceFilterWaiting  workspaceFilter = "waiting"
	workspaceFilterIdle     workspaceFilter = "idle"
	workspaceFilterError    workspaceFilter = "error"
	workspaceFilterOpen     workspaceFilter = "open"
	workspaceFilterArchived workspaceFilter = "archived"
)

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
type chromeFinishedMsg struct {
	key string
	err error
}
type systemStatsLoadedMsg struct{ stats sysinfo.Stats }
type systemStatsTickMsg struct{}

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
	m := &sidebarModel{
		profile:        profile,
		controller:     controller,
		allItems:       append([]sidebarItem(nil), items...),
		width:          DefaultSidebarWidth,
		height:         30,
		activeExcludes: (session.DisplaySettings{}).GetActiveFilterExcludes(),
	}
	if cfg, err := session.LoadUserConfig(); err == nil && cfg != nil {
		m.systemStatsConfig = cfg.SystemStats
		m.activeExcludes = cfg.Display.GetActiveFilterExcludes()
	}
	m.rebuildFilteredItems("")
	return m
}

func (m *sidebarModel) Init() tea.Cmd {
	cmds := []tea.Cmd{refreshTickCmd(), m.scheduleSwitch(), m.updateChromeCmd()}
	if m.systemStatsConfig.GetEnabled() {
		cmds = append(cmds, collectSystemStatsCmd())
	}
	return tea.Batch(cmds...)
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
			// A placeholder has nothing interactive to focus. Keeping keyboard
			// focus in the navigator avoids trapping arrow keys in a stopped,
			// archived, remote, or otherwise unavailable session pane.
			if !sessionCanAcceptFocus(item.data) {
				m.busy = ""
				m.err = nil
				return m, nil
			}
			m.busy = "opening " + item.data.Title
			return m, m.focusSelectedCmd(*item)
		case "m":
			return m, m.openManager("", "manager")
		case "n":
			return m, m.openManager("n", "new session")
		case "f":
			return m, m.openManager("f", "quick fork")
		case "F":
			return m, m.openManager("F", "fork options")
		case "/":
			return m, m.openManager("/", "search")
		case "?":
			return m, m.openManager("?", "help")
		case "S":
			return m, m.openManager("S", "settings")
		case "$":
			return m, m.openManager("$", "costs")
		case "t":
			return m, m.openManager("t", "view options")
		case "0":
			return m, m.setFilter(workspaceFilterAll)
		case "!", "shift+1":
			return m, m.toggleFilter(workspaceFilterRunning)
		case "@", "shift+2":
			return m, m.toggleFilter(workspaceFilterWaiting)
		case "#", "shift+3":
			return m, m.toggleFilter(workspaceFilterIdle)
		case "&", "shift+7":
			return m, m.toggleFilter(workspaceFilterError)
		case "%", "shift+5":
			return m, m.toggleFilter(workspaceFilterOpen)
		case "^", "shift+6":
			return m, m.toggleFilter(workspaceFilterArchived)
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
			return m, tea.Batch(m.scheduleSwitch(), m.updateChromeCmd())
		}
		if item == nil && m.activeID != "" {
			return m, tea.Batch(m.scheduleSwitch(), m.updateChromeCmd())
		}
		return m, m.updateChromeCmd()

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

	case chromeFinishedMsg:
		if msg.err != nil {
			m.lastChrome = ""
			m.err = msg.err
		}

	case systemStatsLoadedMsg:
		m.systemStats = msg.stats
		return m, tea.Batch(m.updateChromeCmd(), systemStatsTickCmd(m.systemStatsConfig.GetRefreshSeconds()))

	case systemStatsTickMsg:
		return m, collectSystemStatsCmd()

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

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#787fa0"))
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color("#7aa2f7")).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e"))
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Bold(true)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#414868"))
	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Bold(true)

	lines := []string{
		fitLine(titleStyle.Render("SESSIONS"), width),
		borderStyle.Render(strings.Repeat("─", maxInt(1, width))),
	}

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
			running, waiting, count := m.groupCounts(group)
			groupStatus := ""
			if running > 0 {
				groupStatus += activeStyle.Render(fmt.Sprintf(" ● %d", running))
			}
			if waiting > 0 {
				groupStatus += lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")).Render(fmt.Sprintf(" ◐ %d", waiting))
			}
			groupLine := groupStyle.Render(fmt.Sprintf("%d·▾ %s (%d)", m.groupNumber(group), group, count)) + groupStatus
			contentLines = append(contentLines, fitLine(groupLine, width))
			lastGroup = group
		}
		if len(contentLines) >= contentLimit {
			break
		}

		cursor := "  "
		if index == m.cursor {
			cursor = "▸ "
		}
		active := ""
		if item.data.ID == m.activeID {
			active = activeStyle.Render("◆")
		} else {
			active = "├"
		}
		status := statusGlyph(item.data)
		tool := strings.TrimSpace(item.data.Tool)
		if tool == "" {
			tool = "shell"
		}
		titleWidth := maxInt(4, width-9-utf8.RuneCountInString(tool))
		line := fmt.Sprintf("%s%s─ %s %s %s", cursor, active, status, truncateRunes(item.data.Title, titleWidth), tool)
		line = fitLine(line, width)
		if index == m.cursor {
			line = selectedStyle.Width(width).Render(line)
		}
		contentLines = append(contentLines, line)
	}
	if len(m.items) == 0 {
		contentLines = append(contentLines, " No managed sessions", "", " Press m to open manager")
	}
	for len(contentLines) < contentLimit {
		contentLines = append(contentLines, "")
	}
	lines = append(lines, contentLines[:contentLimit]...)

	status := "↑↓ switch · enter focus"
	if m.busy != "" {
		status = m.busy
	} else if m.err != nil {
		status = errorStyle.Render(truncateRunes(m.err.Error(), maxInt(1, width)))
	}
	lines = append(lines,
		fitLine(status, width),
		fitLine(dimStyle.Render("n new · f/F fork · m tools · q detach"), width),
	)
	return strings.Join(lines, "\n")
}

func (m *sidebarModel) openManager(action, label string) tea.Cmd {
	instanceID := ""
	if item := m.selected(); item != nil && item.data != nil {
		instanceID = item.data.ID
	}
	return m.execZoomed(m.controller.ManagerCommand(instanceID, action), label)
}

func (m *sidebarModel) setFilter(filter workspaceFilter) tea.Cmd {
	selectedID := ""
	if selected := m.selected(); selected != nil {
		selectedID = selected.data.ID
	}
	m.filter = filter
	m.rebuildFilteredItems(selectedID)
	m.err = nil
	return tea.Batch(m.scheduleSwitch(), m.updateChromeCmd())
}

func (m *sidebarModel) toggleFilter(filter workspaceFilter) tea.Cmd {
	if m.filter == filter {
		filter = workspaceFilterAll
	}
	return m.setFilter(filter)
}

func sessionCanAcceptFocus(inst *session.InstanceData) bool {
	if inst == nil || !inst.ArchivedAt.IsZero() || strings.TrimSpace(inst.SSHHost) != "" || strings.TrimSpace(inst.TmuxSession) == "" {
		return false
	}
	switch inst.Status {
	case session.StatusStopped, session.StatusError:
		return false
	default:
		return true
	}
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
	m.allItems = append([]sidebarItem(nil), items...)
	m.rebuildFilteredItems(selectedID)
	items = m.items
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

func (m *sidebarModel) rebuildFilteredItems(selectedID string) {
	m.items = m.items[:0]
	for _, item := range m.allItems {
		if item.data == nil {
			continue
		}
		archived := !item.data.ArchivedAt.IsZero()
		matches := false
		switch m.filter {
		case workspaceFilterArchived:
			matches = archived
		case workspaceFilterOpen:
			matches = !archived && !m.activeExcludes[item.data.Status]
		case workspaceFilterRunning, workspaceFilterWaiting, workspaceFilterIdle, workspaceFilterError:
			matches = !archived && string(item.data.Status) == string(m.filter)
		default:
			matches = !archived
		}
		if matches {
			m.items = append(m.items, item)
		}
	}
	if len(m.items) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if selectedID != "" {
		for index := range m.items {
			if m.items[index].data.ID == selectedID {
				m.cursor = index
				m.ensureCursorVisible()
				return
			}
		}
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	m.ensureCursorVisible()
}

func (m *sidebarModel) ensureCursorVisible() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Two title rows and two footer rows surround one row per item. Group
	// headers consume a little more room; the conservative extra row prevents
	// the selected session from slipping under the footer.
	visibleItems := maxInt(1, m.height-5)
	if m.cursor >= m.offset+visibleItems {
		m.offset = m.cursor - visibleItems + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *sidebarModel) groupCounts(group string) (running, waiting, count int) {
	for _, item := range m.items {
		if item.data == nil || normalizedGroup(item.data) != group {
			continue
		}
		count++
		switch item.data.Status {
		case session.StatusRunning:
			running++
		case session.StatusWaiting:
			waiting++
		}
	}
	return running, waiting, count
}

func (m *sidebarModel) groupNumber(group string) int {
	number, last := 0, "\x00"
	for _, item := range m.items {
		if item.data == nil {
			continue
		}
		current := normalizedGroup(item.data)
		if current != last {
			number++
			last = current
		}
		if current == group {
			return number
		}
	}
	return 1
}

func normalizedGroup(inst *session.InstanceData) string {
	if inst == nil || strings.TrimSpace(inst.GroupPath) == "" {
		return "Ungrouped"
	}
	return strings.TrimSpace(inst.GroupPath)
}

func (m *sidebarModel) statusCounts() (running, waiting, idle, stopped, errored int) {
	for _, item := range m.allItems {
		if item.data == nil || !item.data.ArchivedAt.IsZero() {
			continue
		}
		switch item.data.Status {
		case session.StatusRunning:
			running++
		case session.StatusWaiting:
			waiting++
		case session.StatusIdle:
			idle++
		case session.StatusStopped:
			stopped++
		case session.StatusError:
			errored++
		}
	}
	return
}

func (m *sidebarModel) chrome() (string, string) {
	running, waiting, idle, stopped, errored := m.statusCounts()
	accent := "#[fg=#7aa2f7,bold]"
	dim := "#[fg=#787fa0]"
	text := "#[fg=#c0caf5]"
	green := "#[fg=#9ece6a]"
	yellow := "#[fg=#e0af68]"
	red := "#[fg=#f7768e]"
	reset := "#[default]"

	title := "Agent Deck"
	if m.profile != "" && m.profile != session.DefaultProfile {
		title += " [" + escapeTmuxText(m.profile) + "]"
	}
	parts := []string{accent + "⟨ " + statusGlyphForCount(running, waiting, idle, 0) + " │ " + statusGlyphForCount(running, waiting, idle, 1) + " │ " + statusGlyphForCount(running, waiting, idle, 2) + " ⟩", accent + " " + title}
	if running > 0 {
		parts = append(parts, green+fmt.Sprintf("● %d running", running))
	}
	if waiting > 0 {
		parts = append(parts, yellow+fmt.Sprintf("◐ %d waiting", waiting))
	}
	if idle > 0 {
		parts = append(parts, text+fmt.Sprintf("○ %d idle", idle))
	}
	if stopped > 0 {
		parts = append(parts, dim+fmt.Sprintf("■ %d stopped", stopped))
	}
	if errored > 0 {
		parts = append(parts, red+fmt.Sprintf("✕ %d error", errored))
	}
	if m.systemStatsConfig.GetEnabled() {
		if stats := sysinfo.Format(m.systemStats, m.systemStatsConfig.GetFormat(), m.systemStatsConfig.GetShow()); stats != "" {
			parts = append(parts, dim+escapeTmuxText(stats))
		}
	}
	header := "#[align=left] " + strings.Join(parts, dim+" │ ") + reset

	pill := func(label string, filter workspaceFilter) string {
		if m.filter == filter {
			return "#[fg=#1a1b26,bg=#7aa2f7,bold] " + label + " " + reset
		}
		return "#[fg=#c0caf5,bg=#24283b] " + label + " " + reset
	}
	filters := "#[align=left] " + pill("ALL", workspaceFilterAll) + " " +
		pill(fmt.Sprintf("● %d", running), workspaceFilterRunning) + " " +
		pill(fmt.Sprintf("◐ %d", waiting), workspaceFilterWaiting) + " " +
		pill(fmt.Sprintf("○ %d", idle), workspaceFilterIdle) + " " +
		pill(fmt.Sprintf("✕ %d", errored), workspaceFilterError) +
		dim + "  !@##& filter • 0 all • %% open • ^ archived • t view" + reset
	return header, filters
}

func (m *sidebarModel) updateChromeCmd() tea.Cmd {
	header, filters := m.chrome()
	key := header + "\x00" + filters
	if key == m.lastChrome {
		return nil
	}
	m.lastChrome = key
	return func() tea.Msg {
		return chromeFinishedMsg{key: key, err: m.controller.UpdateChrome(context.Background(), header, filters)}
	}
}

func collectSystemStatsCmd() tea.Cmd {
	return func() tea.Msg { return systemStatsLoadedMsg{stats: sysinfo.Collect()} }
}

func systemStatsTickCmd(seconds int) tea.Cmd {
	if seconds < 2 {
		seconds = 5
	}
	return tea.Tick(time.Duration(seconds)*time.Second, func(time.Time) tea.Msg { return systemStatsTickMsg{} })
}

func statusGlyphForCount(running, waiting, idle, index int) string {
	values := make([]string, 0, 3)
	for n := 0; n < running && len(values) < 3; n++ {
		values = append(values, "#[fg=#9ece6a]●")
	}
	for n := 0; n < waiting && len(values) < 3; n++ {
		values = append(values, "#[fg=#e0af68]◐")
	}
	for n := 0; n < idle && len(values) < 3; n++ {
		values = append(values, "#[fg=#c0caf5]○")
	}
	for len(values) < 3 {
		values = append(values, "#[fg=#787fa0]○")
	}
	return values[index]
}

func escapeTmuxText(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	return strings.ReplaceAll(value, "#", "##")
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
		return "◐"
	case session.StatusStarting, session.StatusQueued:
		return "◐"
	case session.StatusIdle:
		return "○"
	case session.StatusError:
		return "✕"
	case session.StatusStopped:
		return "■"
	default:
		return "·"
	}
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	plainWidth := lipgloss.Width(value)
	if plainWidth > width {
		return ansi.Truncate(value, width, "…")
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
