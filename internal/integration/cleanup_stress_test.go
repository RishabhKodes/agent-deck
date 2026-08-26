package integration

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// =============================================================================
// Resource Cleanup Tests (Phase 16)
// =============================================================================

// TestCleanup_NoOrphanedTmuxAfterKill verifies that Kill() leaves no orphaned
// tmux sessions. Creates 5 sessions, kills them all, then scans tmux for any
// sessions matching the test prefix.
func TestCleanup_NoOrphanedTmuxAfterKill(t *testing.T) {
	h := NewTmuxHarness(t)

	const count = 5
	var tmuxNames []string
	instances := make([]*session.Instance, 0, count)

	for i := range count {
		inst := h.CreateSession(fmt.Sprintf("cleanup-%02d", i), "/tmp")
		inst.Command = "sleep 60"
		require.NoError(t, inst.Start(), "session %d Start() should succeed", i)
		instances = append(instances, inst)
	}

	// Wait for all sessions to exist
	for i, inst := range instances {
		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("session %d to exist", i),
			func() bool { return inst.Exists() })
		tmuxNames = append(tmuxNames, inst.GetTmuxSession().Name)
	}

	// Kill all sessions
	for _, inst := range instances {
		require.NoError(t, inst.Kill())
	}

	// Give tmux a moment to clean up
	time.Sleep(500 * time.Millisecond)

	// Verify no orphaned tmux sessions with our prefix
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux server may have no sessions at all, which is fine
		return
	}

	liveSessions := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, name := range tmuxNames {
		for _, live := range liveSessions {
			if live == name {
				t.Errorf("orphaned tmux session found after Kill: %s", name)
			}
		}
	}
}

// TestCleanup_GoroutineStability verifies that starting and stopping sessions
// does not leak goroutines. Measures goroutine count before and after.
func TestCleanup_GoroutineStability(t *testing.T) {
	h := NewTmuxHarness(t)

	// Warm up: let any lazy initialization settle
	warmup := h.CreateSession("goroutine-warmup", "/tmp")
	warmup.Command = "sleep 5"
	require.NoError(t, warmup.Start())
	WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
		"warmup session to exist",
		func() bool { return warmup.Exists() })
	require.NoError(t, warmup.Kill())
	time.Sleep(500 * time.Millisecond)

	// Measure baseline goroutine count
	runtime.GC()
	baseline := runtime.NumGoroutine()

	// Create, use, and destroy 3 sessions
	for i := range 3 {
		inst := h.CreateSession(fmt.Sprintf("goroutine-%d", i), "/tmp")
		inst.Command = "sleep 30"
		require.NoError(t, inst.Start())
		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("session %d to exist", i),
			func() bool { return inst.Exists() })

		// Exercise UpdateStatus
		time.Sleep(300 * time.Millisecond)
		_ = inst.UpdateStatus()

		require.NoError(t, inst.Kill())
	}

	// Allow goroutines to wind down
	time.Sleep(1 * time.Second)
	runtime.GC()
	after := runtime.NumGoroutine()

	// Allow a generous margin (10 goroutines) since Go runtime and test framework
	// goroutines can fluctuate. The key assertion: goroutines should not grow unbounded.
	maxAllowed := baseline + 10
	assert.LessOrEqual(t, after, maxAllowed,
		"goroutine count should not grow significantly: baseline=%d, after=%d", baseline, after)
}

// TestCleanup_KilledSessionReportsCorrectStatus verifies that after Kill(),
// the session correctly reports StatusStopped and Exists() returns false,
// with no stale cached state.
func TestCleanup_KilledSessionReportsCorrectStatus(t *testing.T) {
	h := NewTmuxHarness(t)

	inst := h.CreateSession("cleanup-status", "/tmp")
	inst.Command = "sleep 60"
	require.NoError(t, inst.Start())

	WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
		"session to exist",
		func() bool { return inst.Exists() })

	// Wait past the 1.5s startup grace period before killing so that
	// UpdateStatus won't return StatusStarting from the grace window.
	time.Sleep(2 * time.Second)

	require.NoError(t, inst.Kill())

	assert.Equal(t, session.StatusStopped, inst.GetStatusThreadSafe())
	assert.False(t, inst.Exists())

	// Force next status check to bypass the errorRecheckInterval optimization
	// that would skip re-checking a recently stopped session.
	inst.ForceNextStatusCheck()
	_ = inst.UpdateStatus()

	// After Kill(), UpdateStatus should stay stopped (tmux session gone, Kill sets stopped).
	// The UpdateStatus code at line 2188 preserves StatusStopped when !Exists().
	assert.Equal(t, session.StatusStopped, inst.GetStatusThreadSafe(),
		"UpdateStatus on killed session should preserve stopped status")
}

// =============================================================================
// Concurrent Stress Tests (Phase 16)
// =============================================================================

// TestStress_ConcurrentStartStop starts 5 sessions concurrently, then stops
// them all concurrently. Verifies no deadlocks or races under -race detector.
func TestStress_ConcurrentStartStop(t *testing.T) {
	h := NewTmuxHarness(t)

	const sessionCount = 5
	instances := make([]*session.Instance, sessionCount)

	// Create all instances first (serial, fast)
	for i := range sessionCount {
		instances[i] = h.CreateSession(fmt.Sprintf("stress-startstop-%02d", i), "/tmp")
		instances[i].Command = "sleep 60"
	}

	// Start all concurrently
	g, _ := errgroup.WithContext(context.Background())
	for i := range sessionCount {
		i := i
		g.Go(func() error {
			return instances[i].Start()
		})
	}
	require.NoError(t, g.Wait(), "concurrent Start() should not error")

	// Wait for all to exist
	for i, inst := range instances {
		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("session %d to exist", i),
			func() bool { return inst.Exists() })
	}

	// Stop all concurrently
	g2, _ := errgroup.WithContext(context.Background())
	for i := range sessionCount {
		i := i
		g2.Go(func() error {
			return instances[i].Kill()
		})
	}
	require.NoError(t, g2.Wait(), "concurrent Kill() should not error")

	// Verify all stopped
	for i, inst := range instances {
		assert.Equal(t, session.StatusStopped, inst.GetStatusThreadSafe(),
			"session %d should be stopped", i)
		assert.False(t, inst.Exists(),
			"session %d tmux should not exist after Kill", i)
	}
}

// TestStress_ConcurrentUpdateStatus runs UpdateStatus concurrently on 6
// live sessions, exercising the Instance mutex and tmux cache under load.
// This is a stronger version of TestEdge_ConcurrentPolling with more aggressive
// concurrent pressure.
func TestStress_ConcurrentUpdateStatus(t *testing.T) {
	h := NewTmuxHarness(t)

	const sessionCount = 6
	instances := make([]*session.Instance, 0, sessionCount)

	for i := range sessionCount {
		inst := h.CreateSession(fmt.Sprintf("stress-update-%02d", i), "/tmp")
		inst.Command = "sleep 60"
		require.NoError(t, inst.Start())
		instances = append(instances, inst)
	}

	// Wait for all to exist
	for i, inst := range instances {
		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("session %d to exist", i),
			func() bool { return inst.Exists() })
	}

	// Wait past 1.5s grace period
	time.Sleep(2 * time.Second)

	// Hammer UpdateStatus from many goroutines concurrently
	g, _ := errgroup.WithContext(context.Background())
	for _, inst := range instances {
		inst := inst
		// Each session gets 3 concurrent updaters
		for range 3 {
			g.Go(func() error {
				for range 5 {
					if err := inst.UpdateStatus(); err != nil {
						return err
					}
					time.Sleep(50 * time.Millisecond)
				}
				return nil
			})
		}
	}
	require.NoError(t, g.Wait(), "concurrent UpdateStatus should not error or deadlock")

	// All sessions should be in a valid state
	for i, inst := range instances {
		s := inst.GetStatusThreadSafe()
		assert.NotEqual(t, session.StatusError, s,
			"session %d should not be error, got %q", i, s)
	}
}

// TestStress_ConcurrentSendAndStatus starts 5 sessions, then concurrently
// sends messages and polls status on all of them.
func TestStress_ConcurrentSendAndStatus(t *testing.T) {
	h := NewTmuxHarness(t)

	const sessionCount = 5
	instances := make([]*session.Instance, 0, sessionCount)

	for i := range sessionCount {
		inst := h.CreateSession(fmt.Sprintf("stress-send-%02d", i), "/tmp")
		inst.Command = "cat" // cat echoes input, good for send verification
		require.NoError(t, inst.Start())
		instances = append(instances, inst)
	}

	// Wait for all to exist
	for i, inst := range instances {
		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("session %d to exist", i),
			func() bool { return inst.Exists() })
	}

	// Allow cat to initialize
	time.Sleep(1 * time.Second)

	var wg sync.WaitGroup

	// Send messages concurrently
	for i, inst := range instances {
		wg.Add(1)
		go func(idx int, inst *session.Instance) {
			defer wg.Done()
			tmuxSess := inst.GetTmuxSession()
			if tmuxSess == nil {
				return
			}
			marker := fmt.Sprintf("stress-marker-%d", idx)
			_ = tmuxSess.SendKeysAndEnter(marker)
		}(i, inst)
	}

	// Poll status concurrently while sends are in-flight
	for _, inst := range instances {
		wg.Add(1)
		go func(inst *session.Instance) {
			defer wg.Done()
			for range 3 {
				_ = inst.UpdateStatus()
				time.Sleep(100 * time.Millisecond)
			}
		}(inst)
	}

	wg.Wait()

	// Verify at least one marker was delivered (proving send path works under load)
	anyDelivered := false
	for i, inst := range instances {
		tmuxSess := inst.GetTmuxSession()
		if tmuxSess == nil {
			continue
		}
		content, err := tmuxSess.CapturePaneFresh()
		if err != nil {
			continue
		}
		marker := fmt.Sprintf("stress-marker-%d", i)
		if strings.Contains(content, marker) {
			anyDelivered = true
			break
		}
	}
	assert.True(t, anyDelivered, "at least one send should be delivered under concurrent load")
}

// TestStress_RapidStartStopCycle rapidly creates, starts, and kills a session
// in a tight loop to test for state machine consistency.
func TestStress_RapidStartStopCycle(t *testing.T) {
	h := NewTmuxHarness(t)

	for cycle := range 3 {
		inst := h.CreateSession(fmt.Sprintf("rapid-cycle-%d", cycle), "/tmp")
		inst.Command = "sleep 30"

		require.NoError(t, inst.Start(), "cycle %d Start failed", cycle)

		WaitForCondition(t, 5*time.Second, 200*time.Millisecond,
			fmt.Sprintf("cycle %d session to exist", cycle),
			func() bool { return inst.Exists() })

		require.NoError(t, inst.Kill(), "cycle %d Kill failed", cycle)
		assert.Equal(t, session.StatusStopped, inst.GetStatusThreadSafe(),
			"cycle %d should be stopped after Kill", cycle)
		assert.False(t, inst.Exists(),
			"cycle %d tmux should not exist after Kill", cycle)
	}
}
