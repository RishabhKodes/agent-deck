package ui

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/send"
	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

func deliverToConductorPane(p guardableConductorPane, msg string) error {
	return deliverToConductorPaneGuarded(p, msg, conductorComposerGuardOptions(), 40, 250*time.Millisecond)
}

// conductorComposerGuardOptions are the production bounds of the watcher/
// health-alert composer guard. Dispatch runs in a goroutine, so a generous
// 5s hold costs nothing on the UI thread; the guard is a single pane capture
// when the composer is empty.
func conductorComposerGuardOptions() send.ComposerGuardOptions {
	return send.ComposerGuardOptions{
		HoldWait:     5 * time.Second,
		PollInterval: 250 * time.Millisecond,
		ClearWait:    time.Second,
		Strip:        tmux.StripANSI,
	}
}

// deliverToConductorPaneGuarded wraps deliverToConductorPaneTuned with the
// issue #1409 composer-draft guard: hold while an operator draft occupies the
// composer; at the bound save-clear it; restore it (typed back, no Enter)
// once the automated delivery is confirmed. When delivery is NOT confirmed
// the draft is intentionally not retyped — the composer may still hold the
// automated message and restoring would recreate the merge this guard exists
// to prevent; the saved draft is logged instead.
func deliverToConductorPaneGuarded(p guardableConductorPane, msg string, guardOpts send.ComposerGuardOptions, maxChecks int, checkDelay time.Duration) error {
	guard := send.GuardComposerDraft(p, guardOpts)
	// guard.ComposerPasteMarkerFree is the #1777 provenance the verify loop
	// needs: the guard's pre-send capture saw a composer with no
	// "[Pasted text …]" marker, so a marker seen during verification is the
	// collapsed rendering of OUR framed multi-line payload (issue #1855).
	err := deliverToConductorPaneAttributed(p, msg, guard.ComposerPasteMarkerFree, maxChecks, checkDelay)
	if guard.SavedDraft != "" {
		if err == nil {
			// Delivery confirmed: type the operator draft back. If the
			// type-back itself fails the draft is no longer on screen — log
			// it so the loss is visible and recoverable, not swallowed.
			if restoreErr := p.SendKeysChunked(guard.SavedDraft); restoreErr != nil {
				uiLog.Warn("conductor_dispatch_draft_restore_failed",
					slog.String("saved_draft", guard.SavedDraft),
					slog.String("error", restoreErr.Error()))
			}
		} else {
			uiLog.Warn("conductor_dispatch_draft_not_restored",
				slog.String("saved_draft", guard.SavedDraft),
				slog.String("error", err.Error()))
		}
	}
	return err
}

// conductorPane is the slice of *tmux.Session that reliable delivery needs.
// Declaring it as an interface keeps the verify-retry loop unit-testable with a
// fake pane (mirrors the sendRetryTarget interface used by the CLI send path).
type conductorPane interface {
	SendKeysAndEnter(string) error
	SendEnter() error
	CapturePaneFresh() (string, error)
	GetStatus() (string, error)
}

// guardableConductorPane extends conductorPane with the surfaces the #1409
// composer guard needs. *tmux.Session satisfies it.
type guardableConductorPane interface {
	conductorPane
	SendCtrlC() error
	SendKeysChunked(string) error
}

// blindEnterCap bounds the fallback Enter presses for agents whose composer is
// not introspectable, so a message that was actually delivered (and the agent
// has since gone idle) is not spammed with empty submissions.
const blindEnterCap = 3

// deliverToConductorPaneTuned is deliverToConductorPaneAttributed without
// pre-send provenance: a paste marker in the composer counts as foreign —
// never nudged, never read as submitted. Kept for callers with no guard
// capture to attribute against.
func deliverToConductorPaneTuned(p conductorPane, msg string, maxChecks int, checkDelay time.Duration) error {
	return deliverToConductorPaneAttributed(p, msg, false, maxChecks, checkDelay)
}

// deliverToConductorPaneAttributed is deliverToConductorPane with the verify
// budget exposed for tests and the #1777 paste-marker provenance explicit;
// production callers use the default budget (~10s). ownPasteMarker carries
// the caller's pre-send observation that the composer held no "[Pasted text …]"
// marker, which is what makes a marker seen during verification attributable
// to this delivery's own bracketed-paste collapse (issue #1855) rather than
// to a foreign paste parked in the composer.
func deliverToConductorPaneAttributed(p conductorPane, msg string, ownPasteMarker bool, maxChecks int, checkDelay time.Duration) error {
	if err := p.SendKeysAndEnter(msg); err != nil {
		return err
	}
	attrib := send.EnterAttribution{Message: msg, OwnPasteMarker: ownPasteMarker}
	sawUnsent := false
	blindEnters := 0
	for i := 0; i < maxChecks; i++ {
		if checkDelay > 0 {
			time.Sleep(checkDelay)
		}

		// Tool-agnostic success: the status detector recognizes claude and codex
		// "active", so a transition to active proves the Enter was accepted and
		// the agent began processing the message.
		if status, err := p.GetStatus(); err == nil && status == "active" {
			return nil
		}

		raw, err := p.CapturePaneFresh()
		if err != nil {
			continue
		}
		content := tmux.StripANSI(raw)

		switch {
		case send.HasUnsentComposerPrompt(content, msg):
			// Introspectable composer still holds the message: the trailing
			// Enter was swallowed, so re-press it. Surface a tmux rejection
			// immediately rather than letting it masquerade as a timeout.
			sawUnsent = true
			if err := p.SendEnter(); err != nil {
				return fmt.Errorf("retry enter: %w", err)
			}
		case send.ComposerHoldsPasteMarker(raw, tmux.StripANSI):
			// The composer holds a "[Pasted text …]" marker instead of the
			// message body. Since the transport frames every multi-line
			// payload as a bracketed paste (issue #1855), this is the NORMAL
			// delivered-but-unsubmitted shape of our own send — but only
			// provenance can say so, because the marker hides the content.
			// NudgeEnter presses Enter only when the collapse is attributable
			// (ownPasteMarker, or a pane with no composer introspection);
			// otherwise it withholds, this case confirms nothing, and the
			// loop times out honestly instead of the old behavior of falling
			// through to "a composer is rendered without our message" and
			// reporting an unsent message as submitted.
			if attrib.NudgeEnter(p, send.Captured(raw), tmux.StripANSI) {
				sawUnsent = true
			}
		case sawUnsent || send.HasCurrentComposerPrompt(content):
			// The composer previously held the message and is now clear, or a
			// composer is rendered without our message: submitted.
			return nil
		case blindEnters < blindEnterCap:
			// No composer introspection (e.g. codex/cursor) and not yet active.
			// Re-press Enter a bounded number of times in case the delayed Enter
			// was dropped, then defer to the status signal above. A visible
			// composer holding foreign content never reaches this arm — the
			// HasCurrentComposerPrompt case above returns first — so this blind
			// Enter cannot submit text nobody authored (#1777 audit).
			blindEnters++
			if err := p.SendEnter(); err != nil {
				return fmt.Errorf("retry enter: %w", err)
			}
		}
	}
	return fmt.Errorf("watcher dispatch not confirmed submitted (status never active, composer still pending) after %s", time.Duration(maxChecks)*checkDelay)
}
