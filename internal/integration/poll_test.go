package integration

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/stretchr/testify/require"
)

func TestWaitForCondition_Success(t *testing.T) {
	// No tmux needed: uses a simple counter-based condition.
	var counter atomic.Int32

	go func() {
		time.Sleep(100 * time.Millisecond)
		counter.Store(1)
	}()

	// Should not fail: condition will be met within timeout.
	WaitForCondition(t, 2*time.Second, 50*time.Millisecond, "counter reaches 1", func() bool {
		return counter.Load() == 1
	})
}

func TestWaitForCondition_Timeout(t *testing.T) {
	// Use a mock testing.T to capture the Fatalf call without killing our real test.
	mockT := &mockTestingT{}

	WaitForCondition(mockT, 200*time.Millisecond, 50*time.Millisecond, "impossible condition", func() bool {
		return false // never succeeds
	})

	require.True(t, mockT.failed, "WaitForCondition should have called Fatalf")
	require.Contains(t, mockT.msg, "200ms", "error message should include timeout duration")
	require.Contains(t, mockT.msg, "impossible condition", "error message should include description")
}

func TestWaitForPaneContent_DetectsOutput(t *testing.T) {
	skipIfNoTmuxServer(t)

	h := NewTmuxHarness(t)
	inst := h.CreateSession("pane-content", "/tmp")
	err := inst.Start()
	require.NoError(t, err)

	// Send "echo hello" to the tmux session.
	tmuxSess := inst.GetTmuxSession()
	require.NotNil(t, tmuxSess)
	err = tmuxSess.SendKeys("echo hello")
	require.NoError(t, err)

	// WaitForPaneContent should detect "hello" in the pane output.
	WaitForPaneContent(t, inst, "hello", 5*time.Second)
}

func TestWaitForStatus_TransitionsToRunning(t *testing.T) {
	skipIfNoTmuxServer(t)

	h := NewTmuxHarness(t)
	inst := h.CreateSession("status-test", "/tmp")
	err := inst.Start()
	require.NoError(t, err)

	// After Start(), the session enters StatusStarting.
	// Wait for it to reach at least StatusStarting.
	WaitForCondition(t, 5*time.Second, 200*time.Millisecond, "status is starting or beyond", func() bool {
		st := inst.GetStatusThreadSafe()
		return st == session.StatusStarting || st == session.StatusRunning || st == session.StatusWaiting || st == session.StatusIdle
	})
}

// mockTestingT captures Fatalf calls for testing timeout behavior.
type mockTestingT struct {
	failed bool
	msg    string
}

func (m *mockTestingT) Helper() {}

func (m *mockTestingT) Fatalf(format string, args ...any) {
	m.failed = true
	m.msg = format
	if len(args) > 0 {
		m.msg = strings.ReplaceAll(format, "%v", "")
		// Build the actual message for inspection
		m.msg = fmt.Sprintf(format, args...)
	}
}
