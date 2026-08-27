package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Issue #1100 (by @ddorman-dn, follow-up to #1098):
//
//	(a) Shift+Enter on a REMOTE session did nothing — the dispatch arm
//	    only handled ItemTypeSession, so the launcher was never
//	    invoked for remote items.
//	(b) Local Shift+Enter opened a new iTerm WINDOW; users expect a
//	    TAB by default.
//
// These tests pin both fixes at the dispatch boundary in home.go.

// withTempAgentDeckHome points $HOME at a fresh tempdir, optionally
// writing a config.toml there, and clears the LoadUserConfig cache so
// the next call reads from the new path. The returned cleanup restores
// both HOME and the cache. Tests must run in t.Cleanup order, so we
// register the cleanup before returning.
func withTempAgentDeckHome(t *testing.T, configTOML string) {
	t.Helper()
	home := setXDGTestHome(t)
	if configTOML != "" {
		writeXDGTestConfig(t, home, configTOML)
	}
}

// armHomeWithOneRemoteSession sets up a Home whose cursor sits on a
// remote session item, mirroring armHomeWithOneSession's contract but
// for the remote dispatch path. The remote is named in $HOME's
// config.toml so the remote Output sender can resolve it.
func armHomeWithOneRemoteSession(t *testing.T) *Home {
	t.Helper()

	withTempAgentDeckHome(t, `
[remotes.lab]
host = "alice@lab.example"
agent_deck_path = "/usr/local/bin/agent-deck"
profile = "work"
`)

	home := NewHome()
	home.width = 120
	home.height = 40
	home.initialLoading = false

	// Synthesize a single remote-session flat item at the cursor.
	home.flatItems = []session.Item{
		{
			Type:       session.ItemTypeRemoteSession,
			RemoteName: "lab",
			RemoteSession: &session.RemoteSessionInfo{
				ID:    "remote-id-xyz",
				Title: "remote session",
			},
		},
	}
	home.cursor = 0
	return home
}

// TestIssue1100_HomeDispatch_ShiftEnterRemoteActivatesOutput ensures remote
// interaction also stays inside the one dashboard screen.
func TestIssue1100_HomeDispatch_ShiftEnterRemoteActivatesOutput(t *testing.T) {
	home := armHomeWithOneRemoteSession(t)

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{shiftEnterMarker}}
	model, _ := home.handleMainKey(keyMsg)
	home = model.(*Home)

	if !home.insertMode || home.insertModeRemoteName != "lab" || home.insertModeRemoteID != "remote-id-xyz" {
		t.Fatalf("remote Output interaction not activated: active=%v remote=%q id=%q",
			home.insertMode, home.insertModeRemoteName, home.insertModeRemoteID)
	}
}

// TestIssue1100_ShiftEnterIgnoresLegacyWindowPreference pins the unified-screen
// contract even for users who still have the retired iTerm preference saved.
func TestIssue1100_ShiftEnterIgnoresLegacyWindowPreference(t *testing.T) {
	withTempAgentDeckHome(t, `
[ui]
iterm_open_as = "window"
`)
	home, _, _ := armHomeWithOneSession(t)

	_, cmd := home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{shiftEnterMarker}})

	if cmd == nil {
		t.Fatal("legacy iterm_open_as disabled the unified dashboard restart")
	}
}
