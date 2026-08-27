// Package workspace implements the two-pane Agent Deck workspace.
//
// Agent processes remain owned by their existing tmux servers. Workspace uses a
// second, private tmux server only as a compositor: a navigator runs on the left
// and a normal tmux client attached to the selected agent runs on the right.
package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"al.essio.dev/pkg/shellescape"
	"golang.org/x/term"

	"github.com/RishabhKodes/agent-deck/internal/session"
	adTmux "github.com/RishabhKodes/agent-deck/internal/tmux"
)

const (
	DefaultSidebarWidth = 32
	MinSidebarWidth     = 24
	MaxSidebarWidth     = 60
	minimumAgentWidth   = 40
)

var ErrNotRunning = errors.New("workspace is not running")

// LaunchOptions configures the default workspace and its explicit
// `agent-deck workspace` alias.
type LaunchOptions struct {
	Profile      string
	SidebarWidth int
	BinaryPath   string
}

// SidebarOptions are passed only to the private navigator subprocess.
type SidebarOptions struct {
	Profile      string
	SidebarWidth int
	BinaryPath   string
	OuterSocket  string
	OuterSession string
	LeftPane     string
	RightPane    string
}

// Controller lets the navigator manipulate only the two outer dashboard
// panes. It never sends lifecycle commands to an agent's tmux server.
type Controller struct {
	actionMu     sync.Mutex
	profile      string
	binaryPath   string
	outerSocket  string
	outerSession string
	leftPane     string
	rightPane    string
}

// AttachTarget is the minimum persisted information needed to attach a native
// tmux client. Titles and paths are intentionally absent from command building.
type AttachTarget struct {
	InstanceID string
	Title      string
	Tool       string
	Status     session.Status
	TmuxName   string
	SocketName string
	SSHHost    string
	Archived   bool
}

type dashboardIdentity struct {
	socket  string
	session string
	owner   string
}

type dashboardPanes struct {
	left  string
	right string
}

// Run creates or repairs a profile-scoped dashboard and attaches the current
// terminal to it. Reattaching moves the singleton dashboard client, but does
// not affect any agent session.
func Run(ctx context.Context, opts LaunchOptions) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for workspace mode: %w", err)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("workspace mode requires an interactive terminal")
	}

	profile, err := session.ResolveProfileForStorage(opts.Profile)
	if err != nil {
		return fmt.Errorf("resolve profile: %w", err)
	}
	binaryPath, err := resolveBinaryPath(opts.BinaryPath)
	if err != nil {
		return err
	}
	width := ClampSidebarWidth(opts.SidebarWidth)
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 120, 40
	}
	width = clampSidebarForTerminal(width, cols)

	id := identityForProfile(profile)
	panes, err := ensureDashboard(ctx, id, profile, binaryPath, width, cols, rows)
	if err != nil {
		return err
	}
	if err := configureDashboard(ctx, id, panes, width); err != nil {
		return err
	}

	cmd := tmuxCommand(ctx, id.socket, "-u", "attach-session", "-d", "-t", id.session)
	cmd.Env = withoutEnv(os.Environ(), "TMUX")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach workspace: %w", err)
	}
	return nil
}

// Stop terminates only the outer dashboard. Inner agent sessions and their
// processes are deliberately on other tmux servers and remain untouched.
func Stop(ctx context.Context, profile string) error {
	resolved, err := session.ResolveProfileForStorage(profile)
	if err != nil {
		return fmt.Errorf("resolve profile: %w", err)
	}
	id := identityForProfile(resolved)
	if err := verifyDashboardOwner(ctx, id); err != nil {
		return err
	}
	if !hasDashboard(ctx, id) {
		return ErrNotRunning
	}
	if out, err := tmuxCommand(ctx, id.socket, "kill-session", "-t", id.session).CombinedOutput(); err != nil {
		return fmt.Errorf("stop workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NewController validates the private process arguments before allowing pane
// mutations. The generated shell commands quote every argument, and session
// titles/project paths are never included.
func NewController(opts SidebarOptions) (*Controller, error) {
	if opts.Profile == "" || opts.BinaryPath == "" || opts.OuterSocket == "" || opts.OuterSession == "" {
		return nil, errors.New("incomplete workspace sidebar configuration")
	}
	if !validPaneID(opts.LeftPane) || !validPaneID(opts.RightPane) {
		return nil, errors.New("invalid workspace pane identifier")
	}
	want := identityForProfile(opts.Profile)
	if opts.OuterSocket != want.socket || opts.OuterSession != want.session {
		return nil, errors.New("workspace identity does not match profile")
	}
	return &Controller{
		profile:      opts.Profile,
		binaryPath:   opts.BinaryPath,
		outerSocket:  opts.OuterSocket,
		outerSession: opts.OuterSession,
		leftPane:     opts.LeftPane,
		rightPane:    opts.RightPane,
	}, nil
}

// ShowInstance replaces only the nested client in the right pane. The target
// agent's tmux session is never killed or respawned.
func (c *Controller) ShowInstance(ctx context.Context, instanceID string) error {
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	return c.showInstance(ctx, instanceID)
}

func (c *Controller) showInstance(ctx context.Context, instanceID string) error {
	if strings.TrimSpace(instanceID) == "" {
		return c.showPlaceholder(ctx)
	}
	command := shellCommand(c.binaryPath, "-p", c.profile, "__workspace-view", "--instance", instanceID)
	if out, err := tmuxCommand(ctx, c.outerSocket, "respawn-pane", "-k", "-t", c.rightPane, command).CombinedOutput(); err != nil {
		return fmt.Errorf("switch workspace viewer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ActivateInstance serializes an explicit Enter action behind any pending
// highlight switch, then focuses the exact target that Enter selected.
func (c *Controller) ActivateInstance(ctx context.Context, instanceID string) error {
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	if err := c.showInstance(ctx, instanceID); err != nil {
		return err
	}
	return c.focusAgent(ctx)
}

func (c *Controller) showPlaceholder(ctx context.Context) error {
	command := shellCommand(c.binaryPath, "-p", c.profile, "__workspace-view")
	if out, err := tmuxCommand(ctx, c.outerSocket, "respawn-pane", "-k", "-t", c.rightPane, command).CombinedOutput(); err != nil {
		return fmt.Errorf("reset workspace viewer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// FocusAgent moves keyboard focus into the native client on the right.
func (c *Controller) FocusAgent(ctx context.Context) error {
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	return c.focusAgent(ctx)
}

func (c *Controller) focusAgent(ctx context.Context) error {
	if out, err := tmuxCommand(ctx, c.outerSocket, "select-pane", "-t", c.rightPane).CombinedOutput(); err != nil {
		return fmt.Errorf("focus agent pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Detach leaves the dashboard and all agents running.
func (c *Controller) Detach(ctx context.Context) error {
	if out, err := tmuxCommand(ctx, c.outerSocket, "detach-client", "-s", c.outerSession).CombinedOutput(); err != nil {
		return fmt.Errorf("detach workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ManagerCommand returns the unchanged Agent Deck TUI, allowed to run inside
// the private outer tmux pane. The caller zooms the pane around tea.ExecProcess.
func (c *Controller) ManagerCommand() *exec.Cmd {
	cmd := exec.Command(c.binaryPath, "-p", c.profile, "manager")
	cmd.Env = append(os.Environ(), "AGENT_DECK_ALLOW_OUTER_TMUX=1", "AGENTDECK_SKIP_UPDATE_CHECK=1")
	return cmd
}

// ClassicAttachCommand is used for remote sessions, which intentionally retain
// the existing full-screen attach path in workspace v1.
func (c *Controller) ClassicAttachCommand(instanceID string) *exec.Cmd {
	cmd := exec.Command(c.binaryPath, "-p", c.profile, "session", "attach", instanceID)
	cmd.Env = append(os.Environ(), "AGENT_DECK_ALLOW_OUTER_TMUX=1", "AGENTDECK_SKIP_UPDATE_CHECK=1")
	return cmd
}

func (c *Controller) SetZoom(ctx context.Context, zoomed bool) error {
	// tmux -Z toggles, so query first and make this operation idempotent.
	out, err := tmuxCommand(ctx, c.outerSocket, "display-message", "-p", "-t", c.leftPane, "#{window_zoomed_flag}").Output()
	if err != nil {
		return err
	}
	isZoomed := strings.TrimSpace(string(out)) == "1"
	if isZoomed == zoomed {
		return nil
	}
	args := []string{"resize-pane", "-t", c.leftPane, "-Z"}
	if out, err := tmuxCommand(ctx, c.outerSocket, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("change workspace zoom: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunViewer resolves a stable instance ID from read-only storage, then runs a
// normal tmux attach client with this pane's stdin/stdout. Its only special key
// is Ctrl+Q, intercepted by the outer tmux server before it reaches this process.
func RunViewer(ctx context.Context, profile, instanceID string) error {
	// This process lives inside the OUTER compositor. Empty inner socket names
	// must resolve to the user's real default tmux server, never inherited $TMUX.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	if strings.TrimSpace(instanceID) == "" {
		return holdViewer("Select a session from the navigator")
	}
	storage, err := session.NewLiveReadOnlyStorageWithProfile(profile)
	if err != nil {
		return holdViewer("Unable to open this profile\n\n" + err.Error())
	}
	defer storage.Close()
	instances, _, err := storage.LoadLite()
	if err != nil {
		return holdViewer("Unable to load sessions\n\n" + err.Error())
	}
	target, ok := FindAttachTarget(instances, instanceID)
	if !ok {
		return holdViewer("This session no longer exists")
	}
	if target.Archived {
		return holdViewer(fmt.Sprintf("%s is archived\n\nOpen the manager with m to restore it.", target.Title))
	}
	if target.TmuxName == "" {
		return holdViewer(fmt.Sprintf("%s is not running\n\nOpen the manager with m to start or resume it.", target.Title))
	}

	if !adTmux.HasSessionOnSocket(target.SocketName, target.TmuxName) {
		return holdViewer(fmt.Sprintf("%s is not running\n\nOpen the manager with m to restart it.", target.Title))
	}

	cmd := adTmux.ExecContext(ctx, target.SocketName, "-u", "attach-session", "-t", target.TmuxName)
	cmd.Env = withoutEnv(os.Environ(), "TMUX", "TMUX_PANE")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return holdViewer(fmt.Sprintf("Disconnected from %s\n\n%s\n\nPress Ctrl+Q to return to the navigator.", target.Title, err))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return holdViewer(fmt.Sprintf("%s exited\n\nPress Ctrl+Q to return to the navigator.", target.Title))
}

// FindAttachTarget is pure so target resolution and stale-row behavior can be
// tested without touching a real Agent Deck profile.
func FindAttachTarget(instances []*session.InstanceData, instanceID string) (AttachTarget, bool) {
	for _, inst := range instances {
		if inst == nil || inst.ID != instanceID {
			continue
		}
		return AttachTarget{
			InstanceID: inst.ID,
			Title:      inst.Title,
			Tool:       inst.Tool,
			Status:     inst.Status,
			TmuxName:   inst.TmuxSession,
			SocketName: inst.TmuxSocketName,
			SSHHost:    inst.SSHHost,
			Archived:   !inst.ArchivedAt.IsZero(),
		}, true
	}
	return AttachTarget{}, false
}

// ClampSidebarWidth applies the persisted workspace setting's public bounds.
func ClampSidebarWidth(width int) int {
	if width <= 0 {
		return DefaultSidebarWidth
	}
	if width < MinSidebarWidth {
		return MinSidebarWidth
	}
	if width > MaxSidebarWidth {
		return MaxSidebarWidth
	}
	return width
}

func clampSidebarForTerminal(width, total int) int {
	width = ClampSidebarWidth(width)
	max := total - minimumAgentWidth - 1
	if max < MinSidebarWidth {
		max = MinSidebarWidth
	}
	if width > max {
		return max
	}
	return width
}

func identityForProfile(profile string) dashboardIdentity {
	identitySource := profile
	if dbPath, err := session.GetDBPathForProfile(profile); err == nil {
		identitySource = filepath.Clean(dbPath)
	}
	digest := sha256.Sum256([]byte(identitySource))
	suffix := fmt.Sprintf("%x", digest[:6])
	uid := strconv.Itoa(os.Getuid())
	return dashboardIdentity{
		socket:  "agent-deck-workspace-" + uid + "-" + suffix,
		session: "deck_workspace_" + suffix,
		owner:   "agent-deck-workspace:v1:" + suffix,
	}
}

func ensureDashboard(ctx context.Context, id dashboardIdentity, profile, binaryPath string, width, cols, rows int) (dashboardPanes, error) {
	if err := verifyDashboardOwner(ctx, id); err != nil {
		return dashboardPanes{}, err
	}
	if hasDashboard(ctx, id) {
		panes, err := storedPanes(ctx, id)
		if err == nil && paneExists(ctx, id.socket, panes.left) && paneExists(ctx, id.socket, panes.right) {
			if paneDead(ctx, id.socket, panes.left) {
				cmd := sidebarCommand(binaryPath, profile, id, panes, width)
				if out, respawnErr := tmuxCommand(ctx, id.socket, "respawn-pane", "-k", "-t", panes.left, cmd).CombinedOutput(); respawnErr != nil {
					return dashboardPanes{}, fmt.Errorf("repair workspace navigator: %w: %s", respawnErr, strings.TrimSpace(string(out)))
				}
			}
			if paneDead(ctx, id.socket, panes.right) {
				cmd := viewerCommand(binaryPath, profile, "")
				if out, respawnErr := tmuxCommand(ctx, id.socket, "respawn-pane", "-k", "-t", panes.right, cmd).CombinedOutput(); respawnErr != nil {
					return dashboardPanes{}, fmt.Errorf("repair workspace viewer: %w: %s", respawnErr, strings.TrimSpace(string(out)))
				}
			}
			return panes, nil
		}
		// The outer dashboard is disposable. Rebuild corrupt layout state while
		// leaving every inner agent server untouched.
		_ = tmuxCommand(ctx, id.socket, "kill-session", "-t", id.session).Run()
	}

	// Keep the first pane alive with the viewer placeholder until both actual
	// pane IDs exist. Starting the sidebar with predicted IDs creates a race:
	// its initial selection can target the not-yet-created right pane, exit, and
	// take the brand-new tmux server down before split-window runs.
	leftCmd := viewerCommand(binaryPath, profile, "")
	out, err := tmuxCommand(ctx, id.socket, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", id.session, "-n", "workspace", "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), leftCmd).CombinedOutput()
	if err != nil {
		return dashboardPanes{}, fmt.Errorf("create workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	left := strings.TrimSpace(string(out))
	rightCmd := viewerCommand(binaryPath, profile, "")
	out, err = tmuxCommand(ctx, id.socket, "split-window", "-h", "-P", "-F", "#{pane_id}", "-t", left, rightCmd).CombinedOutput()
	if err != nil {
		_ = tmuxCommand(ctx, id.socket, "kill-session", "-t", id.session).Run()
		return dashboardPanes{}, fmt.Errorf("create workspace viewer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	right := strings.TrimSpace(string(out))
	panes := dashboardPanes{left: left, right: right}

	// The first sidebar command used predicted pane IDs. Respawn it with the
	// actual IDs before exposing the dashboard to a client.
	leftCmd = sidebarCommand(binaryPath, profile, id, panes, width)
	if out, err = tmuxCommand(ctx, id.socket, "respawn-pane", "-k", "-t", left, leftCmd).CombinedOutput(); err != nil {
		_ = tmuxCommand(ctx, id.socket, "kill-session", "-t", id.session).Run()
		return dashboardPanes{}, fmt.Errorf("initialize workspace navigator: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return panes, nil
}

func configureDashboard(ctx context.Context, id dashboardIdentity, panes dashboardPanes, width int) error {
	commands := [][]string{
		{"set-option", "-g", "@agentdeck_workspace_owner", id.owner},
		{"set-option", "-g", "@agentdeck_workspace_schema", "1"},
		{"set-option", "-t", id.session, "status", "off"},
		{"set-option", "-t", id.session, "prefix", "None"},
		{"set-option", "-t", id.session, "prefix2", "None"},
		// Mouse selection provides a direct fallback for moving between panes.
		{"set-option", "-t", id.session, "mouse", "on"},
		{"set-option", "-t", id.session, "focus-events", "on"},
		{"set-window-option", "-t", id.session, "remain-on-exit", "on"},
		{"set-option", "-s", "escape-time", "0"},
		{"set-option", "-g", "default-terminal", "tmux-256color"},
		{"set-option", "-t", id.session, "@agentdeck_workspace_left", panes.left},
		{"set-option", "-t", id.session, "@agentdeck_workspace_right", panes.right},
		// Ctrl+Q is always a one-way escape hatch to the navigator. Making it
		// unconditional avoids terminal/input-state differences in placeholders
		// and nested agent clients.
		{"bind-key", "-T", "root", "C-q", "select-pane", "-t", panes.left},
		{"set-hook", "-g", "client-resized", "resize-pane -t " + panes.left + " -x " + strconv.Itoa(width)},
		{"resize-pane", "-t", panes.left, "-x", strconv.Itoa(width)},
		{"select-pane", "-t", panes.left},
	}
	for _, args := range commands {
		if out, err := tmuxCommand(ctx, id.socket, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("configure workspace (%s): %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	// Best-effort true-color declaration; older tmux versions may not support
	// terminal-features, while the dashboard remains functional without it.
	_ = tmuxCommand(ctx, id.socket, "set-option", "-ga", "terminal-features", ",xterm*:RGB").Run()
	_ = tmuxCommand(ctx, id.socket, "set-option", "-s", "extended-keys", "on").Run()
	return nil
}

func sidebarCommand(binaryPath, profile string, id dashboardIdentity, panes dashboardPanes, width int) string {
	return shellCommand(binaryPath, "-p", profile, "__workspace-sidebar",
		"--outer-socket", id.socket,
		"--outer-session", id.session,
		"--left-pane", panes.left,
		"--right-pane", panes.right,
		"--sidebar-width", strconv.Itoa(width),
	)
}

func viewerCommand(binaryPath, profile, instanceID string) string {
	args := []string{"-p", profile, "__workspace-view"}
	if instanceID != "" {
		args = append(args, "--instance", instanceID)
	}
	return shellCommand(binaryPath, args...)
}

func shellCommand(binaryPath string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellescape.Quote(binaryPath))
	for _, arg := range args {
		parts = append(parts, shellescape.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func hasDashboard(ctx context.Context, id dashboardIdentity) bool {
	return tmuxCommand(ctx, id.socket, "has-session", "-t", id.session).Run() == nil
}

func verifyDashboardOwner(ctx context.Context, id dashboardIdentity) error {
	// A failed list means the private server does not exist yet.
	if err := tmuxCommand(ctx, id.socket, "list-sessions").Run(); err != nil {
		return nil
	}
	out, err := tmuxCommand(ctx, id.socket, "show-options", "-g", "-qv", "@agentdeck_workspace_owner").Output()
	if err != nil {
		return fmt.Errorf("inspect workspace owner: %w", err)
	}
	owner := strings.TrimSpace(string(out))
	if owner != id.owner {
		return fmt.Errorf("refusing to reuse tmux socket %q: workspace owner marker is %q", id.socket, owner)
	}
	return nil
}

func storedPanes(ctx context.Context, id dashboardIdentity) (dashboardPanes, error) {
	left, err := tmuxCommand(ctx, id.socket, "show-options", "-t", id.session, "-qv", "@agentdeck_workspace_left").Output()
	if err != nil {
		return dashboardPanes{}, err
	}
	right, err := tmuxCommand(ctx, id.socket, "show-options", "-t", id.session, "-qv", "@agentdeck_workspace_right").Output()
	if err != nil {
		return dashboardPanes{}, err
	}
	panes := dashboardPanes{left: strings.TrimSpace(string(left)), right: strings.TrimSpace(string(right))}
	if !validPaneID(panes.left) || !validPaneID(panes.right) {
		return dashboardPanes{}, errors.New("invalid stored pane identifiers")
	}
	return panes, nil
}

func paneExists(ctx context.Context, socket, pane string) bool {
	return tmuxCommand(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_id}").Run() == nil
}

func paneDead(ctx context.Context, socket, pane string) bool {
	out, err := tmuxCommand(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_dead}").Output()
	return err != nil || strings.TrimSpace(string(out)) == "1"
}

func validPaneID(pane string) bool {
	if len(pane) < 2 || pane[0] != '%' {
		return false
	}
	_, err := strconv.Atoi(pane[1:])
	return err == nil
}

func tmuxCommand(ctx context.Context, socket string, args ...string) *exec.Cmd {
	return adTmux.ExecContext(ctx, socket, args...)
}

func resolveBinaryPath(candidate string) (string, error) {
	if candidate == "" {
		var err error
		candidate, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve agent-deck executable: %w", err)
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve agent-deck executable path: %w", err)
	}
	return absolute, nil
}

func withoutEnv(env []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[key]; !skip {
			out = append(out, entry)
		}
	}
	return out
}

func holdViewer(message string) error {
	fmt.Print("\x1b[2J\x1b[H")
	fmt.Println("Agent Deck Workspace")
	fmt.Println()
	fmt.Println(message)
	fmt.Println()
	fmt.Println("Ctrl+Q  navigator")
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(ch)
	<-ch
	return nil
}
