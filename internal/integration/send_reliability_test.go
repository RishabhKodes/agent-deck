package integration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSend_EnterRetryOnRealTmux verifies that SendKeysAndEnter delivers text
// followed by Enter to a real tmux session running cat. This is the baseline
// end-to-end test proving the send path works on real tmux (not mocks).
func TestSend_EnterRetryOnRealTmux(t *testing.T) {
	h := NewTmuxHarness(t)

	inst := h.CreateSession("send-enter-retry", "/tmp")
	inst.Command = "cat"
	require.NoError(t, inst.Start())

	WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
		"session to exist",
		func() bool { return inst.Exists() })

	tmuxSess := inst.GetTmuxSession()
	require.NotNil(t, tmuxSess, "tmux session should not be nil")

	marker := "enter-retry-marker-" + t.Name()
	require.NoError(t, tmuxSess.SendKeysAndEnter(marker))

	// cat echoes input back to pane; verify the marker appears.
	WaitForPaneContent(t, inst, marker, 5*time.Second)

	content, err := tmuxSess.CapturePaneFresh()
	require.NoError(t, err)
	assert.Contains(t, content, marker, "pane should contain the sent marker")
}

// TestSend_RapidSuccessiveSends verifies that two messages sent in quick
// succession (no sleep between) to a cat session both appear in pane content.
// This catches regressions where the second Enter is dropped due to bracketed
// paste timing or PTY buffer races.
func TestSend_RapidSuccessiveSends(t *testing.T) {
	h := NewTmuxHarness(t)

	inst := h.CreateSession("send-rapid", "/tmp")
	inst.Command = "cat"
	require.NoError(t, inst.Start())

	WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
		"session to exist",
		func() bool { return inst.Exists() })

	tmuxSess := inst.GetTmuxSession()
	require.NotNil(t, tmuxSess, "tmux session should not be nil")

	// Send two messages with no sleep between them.
	msg1 := "msg-1-RAPID-" + t.Name()
	msg2 := "msg-2-RAPID-" + t.Name()
	require.NoError(t, tmuxSess.SendKeysAndEnter(msg1))
	require.NoError(t, tmuxSess.SendKeysAndEnter(msg2))

	// Both messages should appear in pane content (cat echoes each).
	WaitForPaneContent(t, inst, msg1, 5*time.Second)
	WaitForPaneContent(t, inst, msg2, 5*time.Second)

	content, err := tmuxSess.CapturePaneFresh()
	require.NoError(t, err)
	assert.Contains(t, content, msg1, "pane should contain first rapid message")
	assert.Contains(t, content, msg2, "pane should contain second rapid message")
}

// TestSend_CodexReadinessSimulation creates a tmux session running a shell
// script that simulates a Codex-like tool: it sleeps, then prints "codex> "
// and enters a read loop. The test verifies:
//  1. Before the prompt appears, PromptDetector("codex").HasPrompt() returns false
//  2. After the prompt appears, HasPrompt() returns true
//  3. After readiness, SendKeysAndEnter delivers text and it's echoed back
func TestSend_CodexReadinessSimulation(t *testing.T) {
	h := NewTmuxHarness(t)

	// Create a temp shell script simulating Codex startup behavior.
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-codex.sh")
	script := `#!/bin/sh
sleep 3
printf "codex> "
while IFS= read -r line; do
  echo "received: $line"
  printf "codex> "
done
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	inst := h.CreateSession("send-codex-ready", tmpDir)
	inst.Command = scriptPath
	require.NoError(t, inst.Start())

	WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
		"session to exist",
		func() bool { return inst.Exists() })

	tmuxSess := inst.GetTmuxSession()
	require.NotNil(t, tmuxSess, "tmux session should not be nil")

	detector := tmux.NewPromptDetector("codex")

	// Immediately after start, the script is sleeping. The prompt should NOT
	// be visible yet.
	content, err := tmuxSess.CapturePaneFresh()
	require.NoError(t, err)
	assert.False(t, detector.HasPrompt(content),
		"codex prompt should NOT be detected while script is sleeping (content: %q)", content)

	// Wait until the codex> prompt appears (script sleeps ~3s).
	WaitForCondition(t, 10*time.Second, 200*time.Millisecond,
		"codex prompt to appear",
		func() bool {
			c, captureErr := tmuxSess.CapturePaneFresh()
			if captureErr != nil {
				return false
			}
			return detector.HasPrompt(c)
		})

	// After readiness confirmed, send text and verify delivery.
	require.NoError(t, tmuxSess.SendKeysAndEnter("hello-codex"))
	WaitForPaneContent(t, inst, "received: hello-codex", 5*time.Second)

	finalContent, err := tmuxSess.CapturePaneFresh()
	require.NoError(t, err)
	assert.Contains(t, finalContent, "received: hello-codex",
		"pane should contain the response from the simulated Codex session")
}
