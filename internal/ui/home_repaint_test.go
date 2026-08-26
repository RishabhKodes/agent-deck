package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Issue #607: v1.5.1 regression — TUI row offset drift when scrolling.
// When full_repaint is enabled the TUI issues tea.ClearScreen on a 2-second
// tick. That tick does not cover the scroll event itself, so drift accumulates
// between clears. This test suite pins the contract that:
//   - under full_repaint, every KeyMsg and mouse wheel event causes the
//     returned tea.Cmd to include a tea.ClearScreen, and
//   - under the default (full_repaint = false), the returned cmd NEVER
//     includes an extra ClearScreen (regression guard for default users).

// containsClearScreen executes cmd and reports whether any yielded message
// (recursively through tea.BatchMsg) is tea.clearScreenMsg.
func containsClearScreen(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	return msgHasClearScreen(msg)
}

func msgHasClearScreen(msg tea.Msg) bool {
	if msg == nil {
		return false
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if containsClearScreen(c) {
				return true
			}
		}
		return false
	}
	return fmt.Sprintf("%T", msg) == "tea.clearScreenMsg"
}

func scrollTestItems() []session.Item {
	return []session.Item{
		{Type: session.ItemTypeSession, Session: &session.Instance{ID: "s1", Title: "S1"}, Level: 0},
		{Type: session.ItemTypeSession, Session: &session.Instance{ID: "s2", Title: "S2"}, Level: 0},
		{Type: session.ItemTypeSession, Session: &session.Instance{ID: "s3", Title: "S3"}, Level: 0},
	}
}

func TestFullRepaint_ClearsOnMouseWheelDown_Issue607(t *testing.T) {
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = true

	_, cmd := h.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})

	if !containsClearScreen(cmd) {
		t.Fatalf("issue #607: expected tea.ClearScreen in returned cmd when fullRepaint=true and user scrolls wheel-down; got cmd=%v", cmd)
	}
}

func TestFullRepaint_ClearsOnMouseWheelUp_Issue607(t *testing.T) {
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.cursor = 2 // so wheel-up has somewhere to go
	h.fullRepaint = true

	_, cmd := h.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})

	if !containsClearScreen(cmd) {
		t.Fatalf("issue #607: expected tea.ClearScreen in returned cmd when fullRepaint=true and user scrolls wheel-up; got cmd=%v", cmd)
	}
}

func TestFullRepaint_ClearsOnKeyNavigation_Issue607(t *testing.T) {
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = true

	_, cmd := h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	if !containsClearScreen(cmd) {
		t.Fatalf("issue #607: expected tea.ClearScreen in returned cmd when fullRepaint=true and user presses j; got cmd=%v", cmd)
	}
}

func TestFullRepaint_NonNavKeyStillClears_Issue607(t *testing.T) {
	// Under fullRepaint every KeyMsg clears — this is the single-rule contract.
	// Any key the user hits can reveal drift (page-up, ctrl+u/d, ctrl+b/f, g, G)
	// so gating on KeyMsg (not the specific key) is by design.
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = true

	_, cmd := h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})

	if !containsClearScreen(cmd) {
		t.Fatalf("issue #607: expected tea.ClearScreen in returned cmd when fullRepaint=true on any KeyMsg; got cmd=%v", cmd)
	}
}

func TestFullRepaint_Disabled_NoClearOnScroll_Issue607(t *testing.T) {
	// Default-user regression guard. With fullRepaint=false, the outer Update
	// wrapper MUST NOT introduce a ClearScreen.
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = false

	_, cmd := h.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})

	if containsClearScreen(cmd) {
		t.Fatalf("regression: expected NO tea.ClearScreen when fullRepaint=false (default); got one — flickers/flashes default users")
	}
}

func TestFullRepaint_Disabled_NoClearOnKey_Issue607(t *testing.T) {
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = false

	_, cmd := h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	if containsClearScreen(cmd) {
		t.Fatalf("regression: expected NO tea.ClearScreen when fullRepaint=false (default) and user presses j; got one")
	}
}

func TestFullRepaint_NonWheelMouseDoesNotClear_Issue607(t *testing.T) {
	// Non-wheel mouse events (clicks, drags) do not scroll — clearing on them
	// would cause unnecessary flicker. This pins that behaviour.
	h := newTestHomeWithItems(100, 30, scrollTestItems())
	h.fullRepaint = true

	_, cmd := h.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 1, Y: 1})

	if containsClearScreen(cmd) {
		t.Fatalf("expected NO tea.ClearScreen for non-wheel mouse (click) under fullRepaint — would cause click-flicker")
	}
}
