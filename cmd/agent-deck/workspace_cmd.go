package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/RishabhKodes/agent-deck/internal/workspace"
)

func handleWorkspaceStop(profile string, args []string) {
	fs := flag.NewFlagSet("workspace stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck [-p profile] workspace stop")
		fmt.Println()
		fmt.Println("Stop a legacy outer workspace. Managed agent sessions keep running.")
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
	fmt.Println("Legacy workspace stopped. Agent sessions are still running.")
}

func handleWorkspaceSidebar(profile string, args []string) {
	fs := flag.NewFlagSet("__workspace-sidebar", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outerSocket := fs.String("outer-socket", "", "internal outer tmux socket")
	outerSession := fs.String("outer-session", "", "internal outer tmux session")
	leftPane := fs.String("left-pane", "", "internal navigator pane")
	inspectorPane := fs.String("inspector-pane", "", "internal inspector pane")
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
		Profile:       profile,
		SidebarWidth:  *sidebarWidth,
		BinaryPath:    binaryPath,
		OuterSocket:   *outerSocket,
		OuterSession:  *outerSession,
		LeftPane:      *leftPane,
		InspectorPane: *inspectorPane,
		RightPane:     *rightPane,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "workspace sidebar: %v\n", err)
	}
}

func handleWorkspaceInspector(profile string, args []string) {
	fs := flag.NewFlagSet("__workspace-inspector", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	instanceID := fs.String("instance", "", "internal Agent Deck instance ID")
	if err := fs.Parse(args); err != nil {
		return
	}
	ctx, cancel := workspaceSignalContext()
	defer cancel()
	if err := workspace.RunInspector(ctx, profile, *instanceID); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "workspace inspector: %v\n", err)
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
