// Phase 3 test infrastructure: a TUI-less web server entry point so that
// Playwright tests can run inside an agent-deck nested tmux session where
// the main `agent-deck web` subcommand refuses to launch (because it falls
// through to the TUI launch path which trips the nested-session guard and
// fails to allocate a TTY under `go run`).
//
// This binary only starts the web server — no TUI, no SQLite mutations
// beyond what the web handlers themselves perform, no tmux interaction.
// It uses the same internal/web.NewServer constructor that the production
// `agent-deck web` subcommand uses.
//
// Usage:
//
//	agent-deck-test-server -listen 127.0.0.1:18420 -profile _test
//
// Profile defaults to AGENTDECK_PROFILE env var if set, else "_test".
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18420", "Listen address for the web server")
	profile := flag.String("profile", os.Getenv("AGENTDECK_PROFILE"), "Profile to load (default: $AGENTDECK_PROFILE or _test)")
	flag.Parse()

	if *profile == "" {
		*profile = "_test"
	}

	menuData := web.NewMemoryMenuData(web.NewSessionDataService(*profile))
	server := web.NewServer(web.Config{
		ListenAddr:   *listen,
		Profile:      *profile,
		ReadOnly:     false,
		WebMutations: true,
		MenuData:     menuData,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	fmt.Printf("agent-deck-test-server: listening on http://%s (profile=%s)\n", server.Addr(), *profile)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	case sig := <-sigCh:
		fmt.Printf("received %s, shutting down\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
}
