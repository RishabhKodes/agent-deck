package ui

import (
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func TestNewHomeDoesNotStartBackgroundWorkersInTests(t *testing.T) {
	if homeBackgroundWorkersEnabled {
		t.Fatal("TestMain must disable Home background workers")
	}
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	if _, err := session.InjectClaudeHooks(claudeConfigDir); err != nil {
		t.Fatalf("install test Claude hooks: %v", err)
	}

	home := NewHome()
	t.Cleanup(func() {
		if home.hookWatcher != nil {
			home.hookWatcher.Stop()
		}
		home.cancel()
		if home.storage != nil {
			_ = home.storage.Close()
		}
	})

	if home.statusWorkerDone != nil {
		t.Fatal("status worker channel is active in test mode")
	}
	if home.storageWatcher != nil {
		t.Fatal("storage watcher started in test mode")
	}
	if home.hookWatcher != nil {
		t.Fatal("hook watcher started in test mode")
	}
}

func TestDisableHomeBackgroundWorkersForTestsRestoresPreviousState(t *testing.T) {
	previous := homeBackgroundWorkersEnabled
	t.Cleanup(func() { homeBackgroundWorkersEnabled = previous })
	homeBackgroundWorkersEnabled = true

	restore := DisableHomeBackgroundWorkersForTests()
	if homeBackgroundWorkersEnabled {
		t.Fatal("background workers remained enabled")
	}
	restore()
	if !homeBackgroundWorkersEnabled {
		t.Fatal("restore did not reinstate the previous setting")
	}
}
