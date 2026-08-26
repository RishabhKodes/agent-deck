package ui

import (
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/feedback"
	tea "github.com/charmbracelet/bubbletea"
)

// stubSyncOptOut replaces the real syncOptOutToConfig function var for the test
// duration and returns both a counter pointer and a restore func. Keeps tests
// from touching ~/.agent-deck/config.toml on the dev machine.
func stubSyncOptOut(t *testing.T) (*int, func()) {
	t.Helper()
	prev := syncOptOutToConfig
	var count int
	syncOptOutToConfig = func() { count++ }
	return &count, func() { syncOptOutToConfig = prev }
}

// ──────────────────────────────────────────────────────────────────
// v1.7.38 Test c — at stepConfirm, pressing 'n' (or Esc, or any key
// other than y/Y) must set a persistent opt-out, not just dismiss.
// Before v1.7.38 this dismissed silently and re-prompted next run.
// ──────────────────────────────────────────────────────────────────
func TestV1738_FeedbackDialog_ConfirmN_SetsOptOut(t *testing.T) {
	syncCount, restore := stubSyncOptOut(t)
	defer restore()
	d, fs := dialogAtStepConfirm(t, "1.7.38", "looks good")

	// State must start enabled.
	if !d.state.FeedbackEnabled {
		t.Fatal("setup: expected FeedbackEnabled=true before confirm-n")
	}

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if d.state.FeedbackEnabled {
		t.Error("confirm-n must set FeedbackEnabled=false (persistent opt-out, v1.7.38)")
	}
	if d.step != stepDismissed {
		t.Errorf("confirm-n should route to stepDismissed, got step=%d", d.step)
	}
	if fs.ghCalls != 0 {
		t.Errorf("confirm-n must NOT post — gh calls=%d", fs.ghCalls)
	}
	if *syncCount == 0 {
		t.Error("confirm-n must mirror opt-out into config.toml (syncOptOutToConfig was never called)")
	}
}

// Esc at stepConfirm behaves the same as 'n' — decline + opt-out.
func TestV1738_FeedbackDialog_ConfirmEsc_SetsOptOut(t *testing.T) {
	_, restore := stubSyncOptOut(t)
	defer restore()
	d, fs := dialogAtStepConfirm(t, "1.7.38", "")

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if d.state.FeedbackEnabled {
		t.Error("confirm-Esc must set FeedbackEnabled=false (persistent opt-out, v1.7.38)")
	}
	if fs.ghCalls != 0 {
		t.Error("confirm-Esc must NOT post")
	}
}

// Confirming 'y' must NOT opt out — regression guard so the v1.7.38
// "decline opts out" change does not bleed into the happy path.
func TestV1738_FeedbackDialog_ConfirmY_DoesNotOptOut(t *testing.T) {
	d, _ := dialogAtStepConfirm(t, "1.7.38", "great")
	if !d.state.FeedbackEnabled {
		t.Fatal("setup: expected FeedbackEnabled=true")
	}

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if !d.state.FeedbackEnabled {
		t.Error("confirm-y must NOT opt out")
	}
	if d.step != stepSent {
		t.Errorf("confirm-y should route to stepSent, got step=%d", d.step)
	}
}

// ──────────────────────────────────────────────────────────────────
// v1.7.38 Test d — Show() on an opted-out state must no-op. This
// guards passive auto-prompts (the TUI post-launch popup) from
// showing after a user has already declined. Explicit ctrl+e re-open
// paths re-enable the state BEFORE calling Show(), so this guard
// never blocks user-initiated flows.
// ──────────────────────────────────────────────────────────────────
func TestV1738_FeedbackDialog_Show_NoOpWhenOptedOut(t *testing.T) {
	d := NewFeedbackDialog()
	optedOut := &feedback.State{FeedbackEnabled: false, MaxShows: 3}
	sender := feedback.NewSender()

	d.Show("1.7.38", optedOut, sender)

	if d.IsVisible() {
		t.Error("Show() on opted-out state must NOT make the dialog visible (v1.7.38)")
	}
}

// Show() on an enabled state still shows — regression guard so the
// v1.7.38 opt-out guard does not break the normal auto-prompt.
func TestV1738_FeedbackDialog_Show_VisibleWhenEnabled(t *testing.T) {
	d := NewFeedbackDialog()
	enabled := &feedback.State{FeedbackEnabled: true, MaxShows: 3}
	d.Show("1.7.38", enabled, feedback.NewSender())

	if !d.IsVisible() {
		t.Error("Show() on enabled state must still make the dialog visible")
	}
}
