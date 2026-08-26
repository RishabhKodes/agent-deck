package ui

// Maestro fleet-supervisor row presentation (Seam A, model-level — see
// internal/ui/TUI_TESTS.md and issue391_tui_test.go for the seam choice).
//
// The maestro session (exact title "conductor-maestro") is THE fleet
// supervisor: its row must be visually distinct from regular conductor
// rows — a gold ⬢ glyph before the title, a gold title, and a gold
// [SUPERVISOR] badge after the tool name. Regular sessions, including
// workers titled "maestro-*", must render exactly as before.

import (
	"strings"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// goldFgSig is the TrueColor escape payload for ColorYellow in the dark
// theme (#e0af68 → 224;175;104). Tests force the TrueColor profile via
// forceTrueColorProfile (issue391_tui_test.go).
const goldFgSig = "38;2;224;175;104"

func TestMaestro_SessionRow_SupervisorPresentation(t *testing.T) {
	inst := &session.Instance{
		ID:    "sess-maestro",
		Title: "conductor-maestro",
	}

	row := renderSingleSessionRow(t, inst)

	if !strings.Contains(row, "⬢") {
		t.Fatalf("maestro row must carry the ⬢ supervisor glyph; got: %q", row)
	}
	if !strings.Contains(row, "[SUPERVISOR]") {
		t.Fatalf("maestro row must carry the [SUPERVISOR] badge; got: %q", row)
	}
	if !strings.Contains(row, goldFgSig) {
		t.Fatalf("maestro row must be tinted gold (ColorYellow %s); got: %q", goldFgSig, row)
	}
}

func TestMaestro_RegularConductorRow_NoSupervisorMarker(t *testing.T) {
	inst := &session.Instance{
		ID:    "sess-conductor",
		Title: "conductor-agent-deck",
	}

	row := renderSingleSessionRow(t, inst)

	if strings.Contains(row, "⬢") || strings.Contains(row, "[SUPERVISOR]") {
		t.Fatalf("regular conductor row must NOT carry supervisor markers; got: %q", row)
	}
}

func TestMaestro_WorkerWithMaestroPrefix_NoSupervisorMarker(t *testing.T) {
	inst := &session.Instance{
		ID:    "sess-worker",
		Title: "maestro-user-test",
	}

	row := renderSingleSessionRow(t, inst)

	if strings.Contains(row, "⬢") || strings.Contains(row, "[SUPERVISOR]") {
		t.Fatalf("worker sessions titled maestro-* must NOT carry supervisor markers; got: %q", row)
	}
}

// Issue #391 user tint stays the stronger signal: an explicit
// Instance.Color must override the maestro gold on the title, while the
// glyph and badge remain.
func TestMaestro_ExplicitColorOverridesGoldTitle(t *testing.T) {
	inst := &session.Instance{
		ID:    "sess-maestro-tinted",
		Title: "conductor-maestro",
		Color: "#FF0000",
	}

	row := renderSingleSessionRow(t, inst)

	if !strings.Contains(row, "38;2;255;0;0") {
		t.Fatalf("explicit Instance.Color must still tint the maestro title; got: %q", row)
	}
	if !strings.Contains(row, "[SUPERVISOR]") {
		t.Fatalf("badge must survive an explicit color tint; got: %q", row)
	}
}
