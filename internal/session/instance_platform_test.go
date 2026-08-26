package session

import (
	"os/exec"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/tmux"
)

// TestSyncSessionIDsFromTmux_Claude verifies that a CLAUDE_SESSION_ID in the tmux
// environment is read into ClaudeSessionID and ClaudeDetectedAt is set.
func TestSyncSessionIDsFromTmux_Claude(t *testing.T) {
	skipIfNoTmuxServer(t)
	skipIfNoClaudeBinary(t)

	inst := NewInstanceWithTool("test-sync-claude", "/tmp", "claude")
	err := inst.Start()
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() { _ = inst.Kill() }()

	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		t.Fatal("tmux session is nil after Start()")
	}

	const testID = "abc-123-claude"
	if err := tmuxSess.SetEnvironment("CLAUDE_SESSION_ID", testID); err != nil {
		t.Fatalf("SetEnvironment failed: %v", err)
	}

	// Clear any existing detection so we can verify the sync sets it fresh
	inst.ClaudeSessionID = ""
	inst.ClaudeDetectedAt = time.Time{}

	inst.SyncSessionIDsFromTmux()

	if inst.ClaudeSessionID != testID {
		t.Errorf("ClaudeSessionID = %q, want %q", inst.ClaudeSessionID, testID)
	}
	if inst.ClaudeDetectedAt.IsZero() {
		t.Error("ClaudeDetectedAt should be set after sync with non-empty CLAUDE_SESSION_ID")
	}
}

// TestSyncSessionIDsFromTmux_AllTools verifies that all four tool env vars are read
// into their respective fields.
func TestSyncSessionIDsFromTmux_AllTools(t *testing.T) {
	skipIfNoTmuxServer(t)
	skipIfNoClaudeBinary(t)

	inst := NewInstanceWithTool("test-sync-all-tools", "/tmp", "claude")
	err := inst.Start()
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() { _ = inst.Kill() }()

	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		t.Fatal("tmux session is nil after Start()")
	}

	envVars := map[string]string{
		"CLAUDE_SESSION_ID":   "claude-id-999",
		"GEMINI_SESSION_ID":   "gemini-id-888",
		"OPENCODE_SESSION_ID": "opencode-id-777",
		"CODEX_SESSION_ID":    "codex-id-666",
	}
	for k, v := range envVars {
		if err := tmuxSess.SetEnvironment(k, v); err != nil {
			t.Fatalf("SetEnvironment(%s) failed: %v", k, err)
		}
	}

	inst.ClaudeSessionID = ""
	inst.GeminiSessionID = ""
	inst.OpenCodeSessionID = ""
	inst.CodexSessionID = ""
	inst.ClaudeDetectedAt = time.Time{}

	inst.SyncSessionIDsFromTmux()

	if inst.ClaudeSessionID != envVars["CLAUDE_SESSION_ID"] {
		t.Errorf("ClaudeSessionID = %q, want %q", inst.ClaudeSessionID, envVars["CLAUDE_SESSION_ID"])
	}
	if inst.GeminiSessionID != envVars["GEMINI_SESSION_ID"] {
		t.Errorf("GeminiSessionID = %q, want %q", inst.GeminiSessionID, envVars["GEMINI_SESSION_ID"])
	}
	if inst.OpenCodeSessionID != envVars["OPENCODE_SESSION_ID"] {
		t.Errorf("OpenCodeSessionID = %q, want %q", inst.OpenCodeSessionID, envVars["OPENCODE_SESSION_ID"])
	}
	if inst.CodexSessionID != envVars["CODEX_SESSION_ID"] {
		t.Errorf("CodexSessionID = %q, want %q", inst.CodexSessionID, envVars["CODEX_SESSION_ID"])
	}
}

// TestSyncSessionIDsFromTmux_NoOverwriteWithEmpty verifies that if a tmux session
// does NOT have CLAUDE_SESSION_ID set, the existing ClaudeSessionID is preserved.
func TestSyncSessionIDsFromTmux_NoOverwriteWithEmpty(t *testing.T) {
	skipIfNoTmuxServer(t)

	inst := NewInstanceWithTool("test-sync-no-overwrite", "/tmp", "claude")
	err := inst.Start()
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() { _ = inst.Kill() }()

	// Do NOT set CLAUDE_SESSION_ID in the tmux env — it should be absent.
	// Set an existing value on the instance.
	const existingID = "existing-claude-id"
	inst.ClaudeSessionID = existingID

	inst.SyncSessionIDsFromTmux()

	if inst.ClaudeSessionID != existingID {
		t.Errorf("ClaudeSessionID = %q, want preserved value %q (must not blank existing ID)", inst.ClaudeSessionID, existingID)
	}
}

// TestSyncSessionIDsFromTmux_OverwriteWithNew verifies that if tmux env has a
// non-empty CLAUDE_SESSION_ID, it overwrites the existing value on the Instance.
func TestSyncSessionIDsFromTmux_OverwriteWithNew(t *testing.T) {
	skipIfNoTmuxServer(t)
	skipIfNoClaudeBinary(t)

	inst := NewInstanceWithTool("test-sync-overwrite", "/tmp", "claude")
	err := inst.Start()
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() { _ = inst.Kill() }()

	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil {
		t.Fatal("tmux session is nil after Start()")
	}

	const newID = "new-val-from-tmux"
	if err := tmuxSess.SetEnvironment("CLAUDE_SESSION_ID", newID); err != nil {
		t.Fatalf("SetEnvironment failed: %v", err)
	}

	inst.ClaudeSessionID = "old-val"

	inst.SyncSessionIDsFromTmux()

	if inst.ClaudeSessionID != newID {
		t.Errorf("ClaudeSessionID = %q, want %q (tmux env must be authoritative)", inst.ClaudeSessionID, newID)
	}
}

// TestSyncSessionIDsFromTmux_NilTmuxSession verifies that calling SyncSessionIDsFromTmux
// on an Instance with a nil tmuxSession does not panic and returns silently.
func TestSyncSessionIDsFromTmux_NilTmuxSession(t *testing.T) {
	// Bare Instance — no Start() called, tmuxSession is assigned but we nil it out.
	inst := &Instance{
		ID:    "bare-inst",
		Title: "bare",
	}
	// tmuxSession defaults to nil in the struct literal — should not panic.
	inst.SyncSessionIDsFromTmux()
}

// TestSyncSessionIDsFromTmux_NonExistentSession verifies that SyncSessionIDsFromTmux
// is a no-op when the Instance has a tmuxSession whose underlying tmux session
// no longer exists (i.e., Exists() returns false).
func TestSyncSessionIDsFromTmux_NonExistentSession(t *testing.T) {
	skipIfNoTmuxServer(t)

	// Create a tmux.Session object with a name that doesn't exist in tmux.
	// tmux.ReconnectSession constructs a Session object; we pass a bogus name.
	sess := tmux.ReconnectSession("agentdeck_test_nonexistent_zzzzzz", "fake", "/tmp", "")

	inst := &Instance{
		ID:              "test-nonexistent",
		Title:           "test-nonexistent",
		ClaudeSessionID: "should-stay",
		tmuxSession:     sess,
	}

	inst.SyncSessionIDsFromTmux()

	// ID must be preserved since session doesn't exist
	if inst.ClaudeSessionID != "should-stay" {
		t.Errorf("ClaudeSessionID = %q, want preserved %q", inst.ClaudeSessionID, "should-stay")
	}
}

// TestStopSavesSessionID simulates the stop-path data flow: create a tmux session,
// inject CLAUDE_SESSION_ID into its environment, then call SyncSessionIDsFromTmux
// (as handleSessionStop does before Kill). Verifies the ID is captured from tmux env.
func TestStopSavesSessionID(t *testing.T) {
	skipIfNoTmuxServer(t)

	const tmuxSessionName = "agentdeck_test_sync_stop_flow"
	const testClaudeID = "stop-test-uuid-abc123"

	// Create a real tmux session with a known name
	err := exec.Command("tmux", "new-session", "-d", "-s", tmuxSessionName).Run()
	if err != nil {
		t.Fatalf("Failed to create tmux session %s: %v", tmuxSessionName, err)
	}
	defer func() {
		_ = exec.Command("tmux", "kill-session", "-t", tmuxSessionName).Run()
	}()

	// Set CLAUDE_SESSION_ID in the tmux environment
	err = exec.Command("tmux", "set-environment", "-t", tmuxSessionName, "CLAUDE_SESSION_ID", testClaudeID).Run()
	if err != nil {
		t.Fatalf("Failed to set-environment: %v", err)
	}

	// Build a Session wrapper that points to the existing tmux session
	sess := tmux.ReconnectSession(tmuxSessionName, "stop-flow-test", "/tmp", "")

	// Build an Instance with empty ClaudeSessionID (simulates PostStartSync timeout)
	inst := &Instance{
		ID:              "stop-flow-test-id",
		Title:           "stop-flow-test",
		ClaudeSessionID: "",
		tmuxSession:     sess,
	}

	// This is what handleSessionStop will call before Kill()
	inst.SyncSessionIDsFromTmux()

	if inst.ClaudeSessionID != testClaudeID {
		t.Errorf("After SyncSessionIDsFromTmux(): ClaudeSessionID = %q, want %q", inst.ClaudeSessionID, testClaudeID)
	}
	if inst.ClaudeDetectedAt.IsZero() {
		t.Error("ClaudeDetectedAt should be set when CLAUDE_SESSION_ID is synced for the first time")
	}
}
