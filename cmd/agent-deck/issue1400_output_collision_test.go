package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Issue #1400 (first half), CLI wiring: the `session output` -q/last-response
// path and `session send --wait`'s waitForFreshOutput must refuse a transcript
// whose claude_session_id is shared by multiple live instances — the same
// collision semantics #1352 wired into `session output --stream`. Before this
// fix both paths silently returned the SAME bytes for every collided session.

// TestWaitForFreshOutput_RefusesCollidingTranscript pins the fail-fast guard:
// with two live peers sharing one claude_session_id + project path, the wait
// must error immediately (no 5s freshness poll against a corrupt transcript).
func TestWaitForFreshOutput_RefusesCollidingTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "proj-1400")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	encodedPath := session.ConvertToClaudeDirName(projectPath)
	projectsDir := filepath.Join(tmpDir, "projects", encodedPath)
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}

	origConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	os.Setenv("CLAUDE_CONFIG_DIR", tmpDir)
	t.Cleanup(func() {
		os.Setenv("CLAUDE_CONFIG_DIR", origConfigDir)
		session.ClearUserConfigCache()
	})
	session.ClearUserConfigCache()

	sharedID := "collision-session-1400"
	writeClaudeJSONL(t, projectsDir, sharedID, "question", "answer", "2026-01-01T00:00:00Z")

	mk := func(id string) *session.Instance {
		inst := session.NewInstance(id, projectPath)
		inst.ID = id
		inst.Tool = "claude"
		inst.Status = session.StatusRunning
		inst.ClaudeSessionID = sharedID
		return inst
	}
	a := mk("inst-a-1400-cli")
	b := mk("inst-b-1400-cli")
	peers := []*session.Instance{a, b}

	// Long timeout on purpose: a missing guard would poll the colliding
	// transcript for the full window; the guard must return well before it.
	setFastFreshOutputConfig(t, 2*time.Second)

	start := time.Now()
	resp, err := waitForFreshOutput(a, time.Now(), peers)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected collision error, got response %q", resp.Content)
	}
	if !strings.Contains(err.Error(), "colliding transcript") {
		t.Fatalf("expected colliding-transcript error, got: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("guard must fail fast, took %v", elapsed)
	}
}

// TestWaitForFreshOutput_UniquePeerStillReads guards against over-blocking:
// a peer with a DIFFERENT session id must not trip the guard.
func TestWaitForFreshOutput_UniquePeerStillReads(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "proj-1400u")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	encodedPath := session.ConvertToClaudeDirName(projectPath)
	projectsDir := filepath.Join(tmpDir, "projects", encodedPath)
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}

	origConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	os.Setenv("CLAUDE_CONFIG_DIR", tmpDir)
	t.Cleanup(func() {
		os.Setenv("CLAUDE_CONFIG_DIR", origConfigDir)
		session.ClearUserConfigCache()
	})
	session.ClearUserConfigCache()

	writeClaudeJSONL(t, projectsDir, "unique-a-1400", "q", "fresh answer", "2026-03-01T00:00:05Z")

	a := session.NewInstance("inst-a-1400-uq", projectPath)
	a.Tool = "claude"
	a.Status = session.StatusRunning
	a.ClaudeSessionID = "unique-a-1400"

	b := session.NewInstance("inst-b-1400-uq", projectPath)
	b.Tool = "claude"
	b.Status = session.StatusRunning
	b.ClaudeSessionID = "unique-b-1400"

	setFastFreshOutputConfig(t, 2*time.Second)

	sentAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	resp, err := waitForFreshOutput(a, sentAt, []*session.Instance{a, b})
	if err != nil {
		t.Fatalf("unique peer must not block: %v", err)
	}
	if resp.Content != "fresh answer" {
		t.Fatalf("expected own transcript content, got %q", resp.Content)
	}
}
