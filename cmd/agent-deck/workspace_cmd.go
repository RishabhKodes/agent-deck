package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/RishabhKodes/agent-deck/internal/workspace"
)

func handleWorkspace(profile string, args []string) {
	if len(args) > 0 && args[0] == "stop" {
		handleWorkspaceStop(profile, args[1:])
		return
	}

	settings := session.GetWorkspaceSettings()
	fs := flag.NewFlagSet("workspace", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sidebarWidth := fs.Int("sidebar-width", settings.GetSidebarWidth(), "Navigator width in columns (24-60)")
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck [-p profile] workspace [options]")
		fmt.Println()
		fmt.Println("Open the native two-pane agent workspace.")
		fmt.Println("Ctrl+Q returns to the navigator; q detaches without stopping agents.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  stop    Stop only the outer workspace dashboard")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "Error: unknown workspace argument: %s\n", fs.Arg(0))
		fs.Usage()
		return
	}

	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolve agent-deck executable: %v\n", err)
		return
	}
	ctx, cancel := workspaceSignalContext()
	defer cancel()
	if err := workspace.Run(ctx, workspace.LaunchOptions{
		Profile:      profile,
		SidebarWidth: *sidebarWidth,
		BinaryPath:   binaryPath,
	}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
}

func handleWorkspaceStop(profile string, args []string) {
	fs := flag.NewFlagSet("workspace stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck [-p profile] workspace stop")
		fmt.Println()
		fmt.Println("Stop the outer dashboard. Managed agent sessions keep running.")
	}
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return
	}
	if err := workspace.Stop(context.Background(), profile); err != nil {
		if errors.Is(err, workspace.ErrNotRunning) {
			fmt.Println("Workspace is not running; agent sessions are unchanged.")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	fmt.Println("Workspace stopped. Agent sessions are still running.")
}

func handleWorkspaceSidebar(profile string, args []string) {
	fs := flag.NewFlagSet("__workspace-sidebar", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outerSocket := fs.String("outer-socket", "", "internal outer tmux socket")
	outerSession := fs.String("outer-session", "", "internal outer tmux session")
	leftPane := fs.String("left-pane", "", "internal navigator pane")
	rightPane := fs.String("right-pane", "", "internal viewer pane")
	sidebarWidth := fs.Int("sidebar-width", workspace.DefaultSidebarWidth, "internal navigator width")
	if err := fs.Parse(args); err != nil {
		return
	}
	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace sidebar: %v\n", err)
		return
	}
	ctx, cancel := workspaceSignalContext()
	defer cancel()
	err = workspace.RunSidebar(ctx, workspace.SidebarOptions{
		Profile:      profile,
		SidebarWidth: *sidebarWidth,
		BinaryPath:   binaryPath,
		OuterSocket:  *outerSocket,
		OuterSession: *outerSession,
		LeftPane:     *leftPane,
		RightPane:    *rightPane,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "workspace sidebar: %v\n", err)
	}
}

func handleWorkspaceView(profile string, args []string) {
	fs := flag.NewFlagSet("__workspace-view", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	instanceID := fs.String("instance", "", "internal Agent Deck instance ID")
	if err := fs.Parse(args); err != nil {
		return
	}
	ctx, cancel := workspaceSignalContext()
	defer cancel()
	if err := workspace.RunViewer(ctx, profile, *instanceID); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "workspace viewer: %v\n", err)
	}
}

func workspaceSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}
