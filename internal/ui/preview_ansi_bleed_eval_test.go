//go:build eval_smoke

package ui

// Behavioral eval for issue #699 — right-pane preview SGR bleed into left pane.
//
// Why this lives in internal/ui/ and not tests/eval/: Go's internal-package rule
// prevents tests/eval/... from importing internal/ui. The eval still runs under
// `-tags eval_smoke` in the eval-smoke CI tier. See tests/eval/README.md.

import (
	"strings"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Issue #699 — Seam-B (teatest-adjacent) eval
//
// Drives the full Home.View() — which includes lipgloss.JoinHorizontal
// combining the left pane, separator, and right pane into terminal rows —
// with a session whose preview cache carries an unclosed SGR highlight.
// This catches the real user-facing failure mode from @javierciccarelli's
// report: a per-row SGR bleed at the horizontal join, not just a
// per-line issue inside the preview renderer.
//
// Invariant under test: for EVERY newline boundary in the rendered frame,
// SGR state must be reset. If any row ends with SGR still active, the next
// row's left-pane content inherits the highlight — that is the bleed.
func TestEval_FullViewDoesNotLeakSGRAcrossRows_Issue699(t *testing.T) {
	h := seamBNewHome()
	h.initialLoading = false

	inst := session.NewInstance("eval-699", t.TempDir())
	inst.Status = session.StatusRunning
	inst.Tool = "claude"

	h.instancesMu.Lock()
	h.instances = []*session.Instance{inst}
	h.instanceByID[inst.ID] = inst
	h.instancesMu.Unlock()
	h.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: inst}}
	h.cursor = 0
	h.setHotkeys(resolveHotkeys(nil))

	// Realistic Ghostty-reproducible failure: a captured Claude input line
	// that opens a bg highlight with no closing reset visible in the window.
	h.previewCacheMu.Lock()
	h.previewCache[inst.ID] = strings.Join([]string{
		"\x1b[43m> tell me about ghostty",
		"(thinking)",
		"\x1b[41mredraw error line",
		"final response text",
	}, "\n")
	h.previewCacheTime[inst.ID] = time.Now()
	h.previewCacheMu.Unlock()

	rendered := h.View()
	if rendered == "" {
		t.Fatal("Home.View() returned empty — test harness misconfigured")
	}

	for i, row := range strings.Split(rendered, "\n") {
		if sgrActiveAt(row) {
			t.Fatalf("row %d ends with SGR active — next row's left-pane content inherits the highlight (#699)\nrow=%q", i, row)
		}
	}
}
