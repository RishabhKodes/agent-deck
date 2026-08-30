package ui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// defaultInsertBatchDuration is the production debounce window for coalescing
// rune-by-rune typing into a single tmux send-keys call (#1094). Picked to
// be small enough that the user can't feel it (~one frame at 60Hz) but large
// enough that bursts of typing collapse into a single send.
//
// Pre-#1102 this also served as the only defense against per-keystroke
// fork+exec cost — but at realistic typing speeds (>15ms between keys) every
// keystroke still landed in its own batch and paid the full fork+exec. The
// persistent KeySender (#1102) makes the per-call cost sub-millisecond, so
// the batch window now only matters for true bursts and can stay small.
const defaultInsertBatchDuration = 15 * time.Millisecond

// insertFlushMsg is dispatched by the tea.Tick scheduled when the first rune
// of a batch is buffered. When it arrives the buffered text is flushed to
// the focused session.
type insertFlushMsg struct{}

// insertPreviewEchoDelay is how long after an insert keystroke we refresh the
// preview pane (#1131). It must be long enough for tmux to execute the
// send-keys and the pane program to echo (~3ms measured end-to-end in
// internal/tmux), with margin, yet short enough to feel instant. The previous
// behaviour only refreshed on the 2s background tick, so a typed character
// could take up to ~2s to appear in the preview — the reported lag.
const insertPreviewEchoDelay = 60 * time.Millisecond

// insertPreviewRefreshMsg is dispatched by the tick scheduled after an insert
// keystroke. Its handler re-fetches the focused session's preview, bypassing
// the normal 2s previewCacheTTL so the user sees their own typing promptly.
type insertPreviewRefreshMsg struct{}

// Output interaction (#1069 feature 1, by @ddorman-dn): modal type-through for
// the TUI. After pressing Enter or `I` on a focused session, keystrokes
// are forwarded directly to that session's tmux pane via send-keys, instead of
// being interpreted as TUI commands. Esc returns to normal mode.

// enterInsertMode arms insert mode if the cursor is on a local session whose
// tmux pane exists. Returns true on success.
// Errors are surfaced via setError so the user sees why nothing happened.
func (h *Home) enterInsertMode() bool {
	target, ok := h.selectedInsertTarget()
	if !ok {
		return false
	}

	// Open the persistent KeySender FIRST so a bring-up failure keeps the TUI
	// in normal mode. If we flipped
	// insertMode=true first and then failed, the user would be stranded.
	ks, err := h.openInsertKeySender(target)
	if err != nil && !errors.Is(err, errInsertNoTmuxSession) {
		// errInsertNoTmuxSession is the recoverable case for local
		// sessions whose pane vanished between selection and enter —
		// surface a clear error.  Other errors fall back to per-call
		// SendKeys (legacy path) so the feature stays usable on
		// environments where the persistent client can't open
		// (e.g., container without `tmux -C` support).
		h.insertKeySender = nil
	} else if err == nil {
		h.insertKeySender = ks
	} else {
		h.setError(fmt.Errorf("output interaction: %w", err))
		return false
	}

	// Issue #1113: defensively reset the rune-batch buffer so any stale
	// content left by a previous insert session (interrupted flush, race
	// with a tick, future regression) never leaks into the new target.
	// exitInsertMode resets the buffer on the way out, but the user-
	// reported flow (type + Enter + Esc + switch session + re-enter)
	// proves we can't trust the exit path as the sole reset point.
	h.insertBuf.Reset()
	h.insertFlushPending = false
	h.resetPreviewScroll()

	h.insertMode = true
	h.insertModeSessionID = target.local.ID
	return true
}

// selectedInsertTarget resolves the row under the cursor to an insertTargetRef
// or returns ok=false (with an error already pushed to the TUI) when the
// selection isn't a valid insert-mode target.
func (h *Home) selectedInsertTarget() (insertTargetRef, bool) {
	if len(h.flatItems) == 0 || h.cursor >= len(h.flatItems) {
		h.setError(fmt.Errorf("output interaction: select a session first"))
		return insertTargetRef{}, false
	}
	item := h.flatItems[h.cursor]
	switch item.Type {
	case session.ItemTypeSession:
		if item.Session == nil {
			h.setError(fmt.Errorf("output interaction: select a session first"))
			return insertTargetRef{}, false
		}
		if item.Session.GetTmuxSession() == nil {
			h.setError(fmt.Errorf("output interaction: session %q has no tmux pane", item.Session.Title))
			return insertTargetRef{}, false
		}
		return insertTargetRef{local: item.Session}, true
	case session.ItemTypeWindow:
		inst := h.getInstanceByID(item.WindowSessionID)
		if inst == nil || inst.GetTmuxSession() == nil {
			h.setError(fmt.Errorf("output interaction: session has no tmux pane"))
			return insertTargetRef{}, false
		}
		return insertTargetRef{local: inst, windowIndex: item.WindowIndex, hasWindow: true}, true
	default:
		h.setError(fmt.Errorf("output interaction: select a session first"))
		return insertTargetRef{}, false
	}
}

// openInsertKeySender invokes the configured KeySender opener (production
// default or test override) for `target`. Pulled out so enterInsertMode is
// readable and tests can stub the opener without touching production code.
func (h *Home) openInsertKeySender(target insertTargetRef) (insertKeySender, error) {
	if h.insertOpenKeySender == nil {
		return nil, nil // legacy fallback path — flushInsertBuf will use SendKeys
	}
	return h.insertOpenKeySender(target)
}

// exitInsertMode returns the TUI to normal navigation mode. Any pending
// keystrokes in the batch buffer are dropped — they should have been flushed
// by the caller via flushInsertBuf() if the user wanted them preserved. The
// persistent KeySender (if any) is closed here, releasing the tmux -C
// subprocess or SSH ControlMaster slot.
func (h *Home) exitInsertMode() {
	h.insertMode = false
	h.insertModeSessionID = ""
	h.insertBuf.Reset()
	h.insertFlushPending = false
	h.resetPreviewScroll()
	if h.insertKeySender != nil {
		_ = h.insertKeySender.Close()
		h.insertKeySender = nil
	}
}

// handleInsertModeKey is the keyboard handler used while insert mode is
// active. Esc exits, Enter sends a newline, and printable runes (and the
// space key) are buffered then flushed in batches to amortize the per-call
// cost of sending keys (#1094 latency, #1102 perf). Backspace, arrow keys,
// Tab, ShiftTab, Ctrl-C, and Ctrl-D are forwarded as tmux named keys so
// users can edit input and navigate menus inside the focused session
// (claude often shows arrow-driven pickers).
func (h *Home) handleInsertModeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		h.flushInsertBuf()
		h.exitInsertMode()
		return h, nil
	case tea.KeyEnter:
		h.flushInsertBuf()
		h.dispatchInsertKey("", true)
		return h, nil
	case tea.KeySpace:
		h.insertBuf.WriteString(" ")
		return h, h.scheduleInsertFlush()
	case tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return h, nil
		}
		h.insertBuf.WriteString(string(msg.Runes))
		return h, h.scheduleInsertFlush()
	case tea.KeyBackspace:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("BSpace")
		return h, nil
	case tea.KeyUp:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("Up")
		return h, nil
	case tea.KeyDown:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("Down")
		return h, nil
	case tea.KeyLeft:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("Left")
		return h, nil
	case tea.KeyRight:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("Right")
		return h, nil
	case tea.KeyTab:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("Tab")
		return h, nil
	case tea.KeyShiftTab:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("BTab")
		return h, nil
	case tea.KeyPgUp:
		h.flushInsertBuf()
		h.scrollPreviewBy(h.previewPageStep())
		return h, nil
	case tea.KeyPgDown:
		h.flushInsertBuf()
		h.scrollPreviewBy(-h.previewPageStep())
		return h, nil
	case tea.KeyHome:
		h.flushInsertBuf()
		h.scrollPreviewToOldest()
		return h, nil
	case tea.KeyEnd:
		h.flushInsertBuf()
		h.resetPreviewScroll()
		return h, nil
	case tea.KeyCtrlC:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("C-c")
		return h, nil
	case tea.KeyCtrlD:
		h.flushInsertBuf()
		h.dispatchInsertNamedKey("C-d")
		return h, nil
	default:
		// Other keys (function keys, more exotic ctrl combos) intentionally
		// dropped — surface them only if a user actually reports needing them.
		return h, nil
	}
}

// scheduleInsertFlush returns a tea.Cmd that will deliver insertFlushMsg
// after the batching window, unless one is already pending or batching is
// disabled (insertBatchDuration <= 0, in which case the buffer flushes
// synchronously and no Cmd is returned).
func (h *Home) scheduleInsertFlush() tea.Cmd {
	if h.insertBatchDuration <= 0 {
		h.flushInsertBuf()
		return nil
	}
	if h.insertFlushPending {
		return nil
	}
	h.insertFlushPending = true
	d := h.insertBatchDuration
	return tea.Tick(d, func(time.Time) tea.Msg { return insertFlushMsg{} })
}

// scheduleInsertPreviewRefresh returns a tea.Cmd that fires insertPreviewRefreshMsg
// after insertPreviewEchoDelay, unless one is already pending. The pending
// guard means a burst of keystrokes arms a single refresh tick rather than one
// per key; once the tick fires (and the flag clears) the next keystroke re-arms
// it, giving a steady ~60ms echo cadence while the user is actively typing.
func (h *Home) scheduleInsertPreviewRefresh() tea.Cmd {
	if h.insertPreviewRefreshPending {
		return nil
	}
	h.insertPreviewRefreshPending = true
	return tea.Tick(insertPreviewEchoDelay, func(time.Time) tea.Msg { return insertPreviewRefreshMsg{} })
}

// flushInsertBuf dispatches any buffered runes to the focused session as a
// single send-keys call, then clears the buffer. Called from the periodic
// timer (insertFlushMsg) and synchronously before any non-rune key (Enter,
// Esc, Backspace, arrows, ...) so the keystroke ordering observed by the
// target pane matches the order in which the user pressed them.
func (h *Home) flushInsertBuf() {
	h.insertFlushPending = false
	if h.insertBuf.Len() == 0 {
		return
	}
	text := h.insertBuf.String()
	h.insertBuf.Reset()
	h.dispatchInsertKey(text, false)
}

// dispatchInsertKey forwards literal text (optionally followed by Enter) to
// the target session. Dispatch order:
//  1. insertKeySink — test override that captures calls
//  2. insertKeySender — production persistent client (local tmux -C OR
//     remote SSH RPC; opened in enterInsertMode)
//  3. legacy fallback — fork+exec one tmux send-keys per call (slow but
//     unconditional; used when the persistent client failed to open)
func (h *Home) dispatchInsertKey(text string, sendEnter bool) {
	// Tests use insertKeySink to inspect calls without running tmux.
	if h.insertKeySink != nil {
		inst := h.resolveInsertTarget()
		if inst == nil {
			return
		}
		if err := h.insertKeySink(inst, text, sendEnter); err != nil {
			h.setError(fmt.Errorf("insert mode send failed: %w", err))
		}
		return
	}

	// Production: prefer the persistent KeySender. One fork+exec at
	// enterInsertMode; per-keystroke calls become stdin writes (local)
	// or amortize over SSH ControlMaster (remote).
	if h.insertKeySender != nil {
		if text != "" {
			if err := h.insertKeySender.SendKeys(text); err != nil {
				h.setError(fmt.Errorf("insert mode send-keys failed: %w", err))
				return
			}
		}
		if sendEnter {
			if err := h.insertKeySender.SendEnter(); err != nil {
				h.setError(fmt.Errorf("insert mode send-enter failed: %w", err))
			}
		}
		return
	}

	// Legacy fallback: per-call fork+exec via the Session's tmux helpers.
	// Hit only when OpenKeySender failed at enterInsertMode (rare). Remote
	// sessions never reach here because they error out at enterInsertMode
	// when no SSHRunner is configured.
	inst := h.resolveInsertTarget()
	if inst == nil {
		return
	}
	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		h.exitInsertMode()
		h.setError(fmt.Errorf("insert mode: tmux session vanished"))
		return
	}
	if text != "" {
		if err := tmuxSess.SendKeys(text); err != nil {
			h.setError(fmt.Errorf("insert mode send-keys failed: %w", err))
			return
		}
	}
	if sendEnter {
		if err := tmuxSess.SendEnter(); err != nil {
			h.setError(fmt.Errorf("insert mode send-enter failed: %w", err))
		}
	}
}

// dispatchInsertNamedKey forwards a tmux named key (Up/Down/Left/Right/Tab/
// BTab/BSpace/C-c/C-d) to the focused session. Same dispatch precedence as
// dispatchInsertKey: test sink → persistent KeySender → legacy fork+exec.
func (h *Home) dispatchInsertNamedKey(key string) {
	if h.insertNamedKeySink != nil {
		inst := h.resolveInsertTarget()
		if inst == nil {
			return
		}
		if err := h.insertNamedKeySink(inst, key); err != nil {
			h.setError(fmt.Errorf("insert mode send named key failed: %w", err))
		}
		return
	}

	if h.insertKeySender != nil {
		if err := h.insertKeySender.SendNamedKey(key); err != nil {
			h.setError(fmt.Errorf("insert mode send-named-key failed: %w", err))
		}
		return
	}

	inst := h.resolveInsertTarget()
	if inst == nil {
		return
	}
	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		h.exitInsertMode()
		h.setError(fmt.Errorf("insert mode: tmux session vanished"))
		return
	}
	if err := tmuxSess.SendNamedKey(key); err != nil {
		h.setError(fmt.Errorf("insert mode send-named-key failed: %w", err))
	}
}

// resolveInsertTarget returns the local Instance for insert mode, or nil if
// the target has disappeared.
func (h *Home) resolveInsertTarget() *session.Instance {
	if h.insertModeSessionID == "" {
		h.exitInsertMode()
		h.setError(fmt.Errorf("insert mode: no target session"))
		return nil
	}
	inst := h.getInstanceByID(h.insertModeSessionID)
	if inst == nil {
		h.exitInsertMode()
		h.setError(fmt.Errorf("insert mode: target session no longer exists"))
		return nil
	}
	return inst
}

// renderInsertModeBar renders the bottom-of-screen indicator shown while
// insert mode is active. It replaces the standard help bar so the indicator
// is visible at every terminal width and so the help text (with its TUI
// navigation hints) doesn't mislead the user into thinking those bindings
// still apply.
func (h *Home) renderInsertModeBar() string {
	borderStyle := lipgloss.NewStyle().Foreground(ColorBorder)
	border := borderStyle.Render(repeatRune('─', max(0, h.width)))

	targetTitle := h.insertTargetDisplayName()

	badge := lipgloss.NewStyle().
		Foreground(ColorBg).
		Background(ColorYellow).
		Bold(true).
		Padding(0, 1).
		Render(" OUTPUT ACTIVE ")

	infoStyle := lipgloss.NewStyle().Foreground(ColorText)
	hintStyle := lipgloss.NewStyle().Foreground(ColorComment)

	line := badge
	if targetTitle != "" {
		line += " " + infoStyle.Render("→ "+targetTitle)
	}
	hint := "Type · Wheel/PgUp/PgDn scroll · Shift-drag selects · Esc navigates"
	if h.previewScrollOffset > 0 {
		hint = "History paused · End returns live · Shift-drag selects · Esc navigates"
	}
	line += "  " + hintStyle.Render(hint)

	return lipgloss.JoinVertical(lipgloss.Left, border, line)
}

// insertTargetDisplayName returns the local session title for the insert-mode
// target.
func (h *Home) insertTargetDisplayName() string {
	if inst := h.getInstanceByID(h.insertModeSessionID); inst != nil {
		return inst.Title
	}
	return ""
}

// repeatRune is a thin wrapper so insert_mode.go doesn't introduce strings
// into the import set just for one call (matches the rest of home.go's
// pattern of building border lines).
func repeatRune(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]rune, n)
	for i := range buf {
		buf[i] = r
	}
	return string(buf)
}
