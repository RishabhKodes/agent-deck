package ui

import "github.com/RishabhKodes/agent-deck/internal/session"

// insertKeySender is the local interface the insert-mode dispatch path uses
// to forward keystrokes. It abstracts the local (tmux control-mode client)
// and remote (SSH RPC) implementations behind a single type so #1102's UI
// changes stay platform-independent and unit-testable.
//
// Mirrors tmux.KeySender deliberately — same shape, decoupled package so the
// UI doesn't pull a tmux symbol into its public surface and tests can supply
// in-process fakes without depending on real tmux or SSH.
type insertKeySender interface {
	SendKeys(text string) error
	SendNamedKey(key string) error
	SendEnter() error
	Close() error
}

// insertTargetRef describes the session insert mode is dispatching to. Either
// a local Instance (with its tmux session bound) or a remote
// (remoteName, remoteID) tuple — never both, never neither.
type insertTargetRef struct {
	// Local target. nil for remote sessions.
	local *session.Instance
	// A selected tmux window row must become active before the session-scoped
	// sender opens. hasWindow distinguishes window 0 from a session row.
	windowIndex int
	hasWindow   bool
}

// defaultInsertOpenKeySender is the production opener wired up by NewHome.
// It selects the local or remote path based on the target, and falls back to
// returning an error so the caller can degrade to per-call SendKeys (#1102).
//
// Note: this is the ONLY place the UI package decides between local and
// remote KeySender backends. Adding a new backend (e.g. Docker-sandboxed) is
// a one-liner here, not a scatter-shot across insert_mode.go.
func defaultInsertOpenKeySender(target insertTargetRef) (insertKeySender, error) {
	if target.local == nil {
		return nil, errInsertNoTarget
	}
	tmuxSess := target.local.GetTmuxSession()
	if tmuxSess == nil {
		return nil, errInsertNoTmuxSession
	}
	if target.hasWindow {
		if err := tmuxSess.SelectWindow(target.windowIndex); err != nil {
			return nil, err
		}
	}
	ks, err := tmuxSess.OpenKeySender()
	if err != nil {
		return nil, err
	}
	return ks, nil
}
