package watcher_test

import (
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/watcher"
)

// TestHealthTracker_FreshTrackerIsHealthy verifies that a new tracker with no activity is Healthy.
func TestHealthTracker_FreshTrackerIsHealthy(t *testing.T) {
	h := watcher.NewHealthTracker("test-watcher", 60)
	state := h.Check()
	if state.Status != watcher.HealthStatusHealthy {
		t.Errorf("expected Healthy for fresh tracker, got %q (message: %q)", state.Status, state.Message)
	}
}

// TestHealthTracker_SilenceDetection verifies that silence beyond maxSilenceMinutes triggers Warning.
func TestHealthTracker_SilenceDetection(t *testing.T) {
	h := watcher.NewHealthTracker("test-watcher", 60)
	// Simulate that an event was received 65 minutes ago
	h.SetLastEventTimeForTest(time.Now().Add(-65 * time.Minute))
	state := h.Check()
	if state.Status != watcher.HealthStatusWarning {
		t.Errorf("expected Warning for 65 min silence (threshold 60), got %q (message: %q)", state.Status, state.Message)
	}
}

// TestHealthTracker_ConsecutiveErrors_Warning verifies 3+ errors produce Warning.
func TestHealthTracker_ConsecutiveErrors(t *testing.T) {
	t.Run("3_errors_produce_warning", func(t *testing.T) {
		h := watcher.NewHealthTracker("test-watcher", 60)
		for i := 0; i < 3; i++ {
			h.RecordError()
		}
		state := h.Check()
		if state.Status != watcher.HealthStatusWarning {
			t.Errorf("expected Warning after 3 errors, got %q", state.Status)
		}
	})

	t.Run("10_errors_produce_error", func(t *testing.T) {
		h := watcher.NewHealthTracker("test-watcher", 60)
		for i := 0; i < 10; i++ {
			h.RecordError()
		}
		state := h.Check()
		if state.Status != watcher.HealthStatusError {
			t.Errorf("expected Error after 10 errors, got %q", state.Status)
		}
	})
}

// TestHealthTracker_RecordEventResetsErrors verifies RecordEvent resets consecutive errors.
func TestHealthTracker_RecordEventResetsErrors(t *testing.T) {
	h := watcher.NewHealthTracker("test-watcher", 60)
	// Record several errors
	for i := 0; i < 5; i++ {
		h.RecordError()
	}
	// Record an event which should reset the error count
	h.RecordEvent()
	state := h.Check()
	if state.Status != watcher.HealthStatusHealthy {
		t.Errorf("expected Healthy after RecordEvent resets errors, got %q", state.Status)
	}
	if state.ConsecutiveErrors != 0 {
		t.Errorf("expected ConsecutiveErrors=0 after RecordEvent, got %d", state.ConsecutiveErrors)
	}
}

// TestHealthTracker_EventsInLastHour verifies sliding window event counting.
func TestHealthTracker_EventsInLastHour(t *testing.T) {
	h := watcher.NewHealthTracker("test-watcher", 60)
	// Record 3 events
	for i := 0; i < 3; i++ {
		h.RecordEvent()
	}
	count := h.EventsInLastHour()
	if count != 3 {
		t.Errorf("expected 3 events in last hour, got %d", count)
	}
}

// TestHealthTracker_AdapterUnhealthyProducesError verifies SetAdapterHealth(false) triggers Error.
func TestHealthTracker_AdapterUnhealthyProducesError(t *testing.T) {
	h := watcher.NewHealthTracker("test-watcher", 60)
	h.SetAdapterHealth(false)
	state := h.Check()
	if state.Status != watcher.HealthStatusError {
		t.Errorf("expected Error when adapter is unhealthy, got %q", state.Status)
	}
}
