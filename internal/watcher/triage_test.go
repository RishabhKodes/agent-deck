package watcher

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
)

// fakeClock is a controllable clock for test determinism.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	// In tests we use real time.After since we don't rely on After for rate-limiter tests.
	// Tests control the window via Now() + Advance().
	go func() {
		<-time.After(d)
		ch <- c.Now()
	}()
	return ch
}

func (c *fakeClock) NewTicker(d time.Duration) *time.Ticker {
	// Delegates to real ticker. Tests control timing via Now()/Advance() for rate limits,
	// and for the reaper tests call scanOnce() directly.
	return time.NewTicker(d)
}

// fakeSpawner records Spawn calls for test assertions.
type fakeSpawner struct {
	mu           sync.Mutex
	calls        []TriageRequest
	resultWriter func(req TriageRequest) // optional: simulates session writing result.json
	err          error                   // optional: return this error from Spawn
}

func (f *fakeSpawner) Spawn(_ context.Context, req TriageRequest) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	f.mu.Unlock()
	if f.resultWriter != nil {
		go f.resultWriter(req)
	}
	return fmt.Sprintf("fake-session-%d", n), nil
}

func (f *fakeSpawner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestRateLimiter_PrunesStaleOnEveryCheck seeds 5 stale spawns, then calls
// tryAcquire — pruning must clear the window and return true, leaving 1 entry.
func TestRateLimiter_PrunesStaleOnEveryCheck(t *testing.T) {
	fc := newFakeClock()
	rl := &rateLimiter{}

	staleTime := fc.Now().Add(-70 * time.Minute) // 70 min ago → outside the 60-min window
	for i := 0; i < 5; i++ {
		rl.spawns = append(rl.spawns, staleTime)
	}

	// tryAcquire must prune all 5 stale entries and allow a new spawn.
	got := rl.tryAcquire(fc.Now())
	if !got {
		t.Fatal("expected tryAcquire to return true after pruning stale entries, got false")
	}
	// After the successful acquire, there should be exactly 1 entry (the one just added).
	if len(rl.spawns) != 1 {
		t.Fatalf("expected 1 entry after pruning and acquire, got %d", len(rl.spawns))
	}
}

// TestTriageLoop_RateLimitSixthQueued_Unit unit-tests just the rateLimiter:
// 6 calls within 1ms must yield 5 true and 1 false.
func TestTriageLoop_RateLimitSixthQueued_Unit(t *testing.T) {
	fc := newFakeClock()
	rl := &rateLimiter{}

	for i := 0; i < 5; i++ {
		got := rl.tryAcquire(fc.Now())
		if !got {
			t.Fatalf("call %d: expected true, got false", i+1)
		}
	}
	// 6th call must be denied.
	got := rl.tryAcquire(fc.Now())
	if got {
		t.Fatal("6th call: expected false, got true")
	}
}

// TestTriagePrompt_Rendering verifies BuildPrompt produces a deterministic prompt
// containing all required fields from 18-RESEARCH.md Q2.
func TestTriagePrompt_Rendering(t *testing.T) {
	event := Event{
		Source:    "mock",
		Sender:    "alice@example.com",
		Subject:   "New order",
		Body:      "Hello world",
		Timestamp: time.Now(),
	}
	clientsList := map[string]ClientEntry{
		"alice@example.com": {Conductor: "client-a", Group: "client-a/inbox", Name: "Client A"},
	}
	resultPath := "/tmp/triage/abc123/result.json"

	rendered, err := BuildPrompt(event, clientsList, resultPath)
	if err != nil {
		t.Fatalf("BuildPrompt error: %v", err)
	}

	// Required fields per 18-RESEARCH.md Q2 template.
	for _, required := range []string{
		"alice@example.com", // sender
		"New order",         // subject
		"Client A",          // known conductor name
		resultPath,          // exact result path
		"OUTPUT PATH:",
		"OUTPUT SCHEMA",
		"route_to",
		"confidence",
		"should_persist",
	} {
		if !containsStr(rendered, required) {
			t.Errorf("rendered prompt missing %q\nrendered:\n%s", required, rendered)
		}
	}

	// Test body truncation at 4000 chars.
	longBody := make([]byte, 5000)
	for i := range longBody {
		longBody[i] = 'x'
	}
	event2 := Event{
		Source:    "mock",
		Sender:    "bob@example.com",
		Subject:   "Big email",
		Body:      string(longBody),
		Timestamp: time.Now(),
	}
	rendered2, err := BuildPrompt(event2, clientsList, resultPath)
	if err != nil {
		t.Fatalf("BuildPrompt error on long body: %v", err)
	}
	// The body in the rendered prompt must be truncated to at most 4000 runes + ellipsis.
	if len(rendered2) > len(rendered)+4100 {
		t.Error("rendered prompt with long body is not truncated appropriately")
	}
	if !containsStr(rendered2, "…") {
		t.Error("truncated body should include ellipsis character")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && findSubstr(s, substr)
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// newTestEngineWithTriage extends newTestEngine with triage dependencies.
// Returns the engine, db, fakeSpawner, fakeClock, and triage temp dir.
func newTestEngineWithTriage(t *testing.T, clients map[string]ClientEntry) (*Engine, *statedb.StateDB, *fakeSpawner, *fakeClock, string) {
	t.Helper()
	db := newTestDB(t)
	router := NewRouter(clients)
	triageDir := t.TempDir()
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	spawner := &fakeSpawner{}
	clock := newFakeClock()
	cfg := EngineConfig{
		DB:                  db,
		Router:              router,
		MaxEventsPerWatcher: 500,
		HealthCheckInterval: 0,
		TriageSpawner:       spawner,
		Clock:               clock,
		TriageDir:           triageDir,
		ClientsPath:         clientsPath,
	}
	engine := NewEngine(cfg)
	return engine, db, spawner, clock, triageDir
}

// TestTriageLoop_SpawnOnUnroutedEvent verifies that an unrouted event causes
// the triageLoop to invoke the spawner with the expected TriageRequest.
func TestTriageLoop_SpawnOnUnroutedEvent(t *testing.T) {
	engine, db, spawner, _, triageDir := newTestEngineWithTriage(t, nil)
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")

	now := time.Now()
	event := Event{Source: "mock", Sender: "unknown@nomatch.com", Subject: "hello", Timestamp: now}
	adapter := &MockAdapter{events: []Event{event}, listenDelay: 5 * time.Millisecond}
	engine.RegisterAdapter("w1", adapter, AdapterConfig{Type: "mock", Name: "test-watcher"}, 60)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for spawner to record one call.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			engine.Stop()
			t.Fatalf("timeout waiting for triage spawn; spawner calls: %d", spawner.callCount())
		case <-time.After(20 * time.Millisecond):
			if spawner.callCount() >= 1 {
				goto done
			}
		}
	}
done:
	engine.Stop()

	spawner.mu.Lock()
	req := spawner.calls[0]
	spawner.mu.Unlock()

	if req.Event.Sender != "unknown@nomatch.com" {
		t.Errorf("expected sender unknown@nomatch.com, got %q", req.Event.Sender)
	}
	if req.WatcherID != "w1" {
		t.Errorf("expected watcherID w1, got %q", req.WatcherID)
	}
	expectedResultPath := filepath.Join(triageDir, event.DedupKey(), "result.json")
	if req.ResultPath != expectedResultPath {
		t.Errorf("expected ResultPath %q, got %q", expectedResultPath, req.ResultPath)
	}
}

// TestTriageLoop_RateLimitSixthQueued verifies that after 5 spawns in the same
// 60-minute window, the 6th event is queued (not spawned).
func TestTriageLoop_RateLimitSixthQueued(t *testing.T) {
	engine, db, spawner, clock, _ := newTestEngineWithTriage(t, nil)

	// Register 6 unique watchers/events from 6 distinct senders.
	for i := 0; i < 6; i++ {
		wID := fmt.Sprintf("w%d", i+1)
		saveTestWatcher(t, db, wID, "watcher-"+wID, "mock")
		sender := fmt.Sprintf("sender%d@test.com", i+1)
		// Use same fixed timestamp so all are within the same window.
		fixedTS := clock.Now()
		event := Event{Source: "mock", Sender: sender, Subject: "test", Timestamp: fixedTS}
		adapter := &MockAdapter{events: []Event{event}, listenDelay: 5 * time.Millisecond}
		engine.RegisterAdapter(wID, adapter, AdapterConfig{Type: "mock", Name: "watcher-" + wID}, 60)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for exactly 5 spawns.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			engine.Stop()
			t.Fatalf("timeout waiting for 5 spawns; got %d", spawner.callCount())
		case <-time.After(30 * time.Millisecond):
			if spawner.callCount() >= 5 {
				goto waitQueue
			}
		}
	}
waitQueue:
	// Give the 6th event time to queue.
	time.Sleep(100 * time.Millisecond)

	calls := spawner.callCount()
	if calls != 5 {
		engine.Stop()
		t.Errorf("expected 5 spawns after rate limit, got %d", calls)
	}

	// Queue should have 1 item.
	if len(engine.triageQueue) != 1 {
		engine.Stop()
		t.Errorf("expected 1 item in triageQueue, got %d", len(engine.triageQueue))
	}

	// Advance clock 61 minutes and pump the queue.
	clock.Advance(61 * time.Minute)
	engine.PumpTriageQueue()

	// Wait for 6th spawn.
	deadline2 := time.After(3 * time.Second)
	for {
		select {
		case <-deadline2:
			engine.Stop()
			t.Fatalf("timeout waiting for 6th spawn after queue drain; got %d", spawner.callCount())
		case <-time.After(30 * time.Millisecond):
			if spawner.callCount() >= 6 {
				goto allDone
			}
		}
	}
allDone:
	engine.Stop()

	if spawner.callCount() < 6 {
		t.Errorf("expected 6 total spawns after window eviction, got %d", spawner.callCount())
	}
}

// TestTriageLoop_RateLimitSeventeenthDropped verifies that after 5 spawns + 16 queued,
// the 22nd event (the 17th that cannot be queued) is marked "triage-dropped".
// The test name "Seventeenth" refers to the 17th queued attempt overflowing the cap-16 queue.
func TestTriageLoop_RateLimitSeventeenthDropped(t *testing.T) {
	// Use a fixed fakeClock so all events land in the same rate-limit window.
	fc := newFakeClock()
	db := newTestDB(t)
	router := NewRouter(nil)
	triageDir := t.TempDir()
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	spawner := &fakeSpawner{}
	cfg := EngineConfig{
		DB:                  db,
		Router:              router,
		MaxEventsPerWatcher: 500,
		HealthCheckInterval: 0,
		TriageSpawner:       spawner,
		Clock:               fc,
		TriageDir:           triageDir,
		ClientsPath:         clientsPath,
	}
	engine := NewEngine(cfg)

	// Fire 22 events: 5 spawn + 16 queue + 1 drop.
	// (TriageMaxPerHour=5, TriageQueueCap=16)
	const totalEvents = TriageMaxPerHour + TriageQueueCap + 1 // = 22
	for i := 0; i < totalEvents; i++ {
		wID := fmt.Sprintf("drop-w%d", i+1)
		saveTestWatcher(t, db, wID, "watcher-"+wID, "mock")
		sender := fmt.Sprintf("drop-sender%d@test.com", i+1)
		ts := fc.Now()
		event := Event{Source: "mock", Sender: sender, Subject: "test", Timestamp: ts}
		adapter := &MockAdapter{events: []Event{event}, listenDelay: 2 * time.Millisecond}
		engine.RegisterAdapter(wID, adapter, AdapterConfig{Type: "mock", Name: "watcher-" + wID}, 60)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the dropped event to appear in the DB.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			engine.Stop()
			t.Fatalf("timeout waiting for triage-dropped event; spawns=%d", spawner.callCount())
		case <-time.After(50 * time.Millisecond):
			var count int
			err := db.DB().QueryRow(
				`SELECT COUNT(*) FROM watcher_events WHERE routed_to = 'triage-dropped'`,
			).Scan(&count)
			if err == nil && count >= 1 {
				goto dropDetected
			}
		}
	}
dropDetected:
	engine.Stop()

	// Spawner should have exactly 5 calls (rate limit cap).
	if spawner.callCount() != 5 {
		t.Errorf("expected 5 spawns, got %d", spawner.callCount())
	}
}

// TestTriageLoop_QueueDrainsAfterWindowEviction verifies that after advancing
// the clock past the window, queued events spawn.
func TestTriageLoop_QueueDrainsAfterWindowEviction(t *testing.T) {
	engine, db, spawner, clock, _ := newTestEngineWithTriage(t, nil)

	// Fire 6 events in the same window.
	for i := 0; i < 6; i++ {
		wID := fmt.Sprintf("drain-w%d", i+1)
		saveTestWatcher(t, db, wID, "watcher-"+wID, "mock")
		sender := fmt.Sprintf("drain-sender%d@test.com", i+1)
		fixedTS := clock.Now()
		event := Event{Source: "mock", Sender: sender, Subject: "test", Timestamp: fixedTS}
		adapter := &MockAdapter{events: []Event{event}, listenDelay: 5 * time.Millisecond}
		engine.RegisterAdapter(wID, adapter, AdapterConfig{Type: "mock", Name: "watcher-" + wID}, 60)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for 5 spawns + 1 queued.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			engine.Stop()
			t.Fatalf("timeout waiting for 5 spawns")
		case <-time.After(30 * time.Millisecond):
			if spawner.callCount() >= 5 && len(engine.triageQueue) == 1 {
				goto drainTest
			}
		}
	}
drainTest:
	// Advance clock 61 minutes and pump queue.
	clock.Advance(61 * time.Minute)
	engine.PumpTriageQueue()

	// Wait for 6th spawn.
	deadline2 := time.After(3 * time.Second)
	for {
		select {
		case <-deadline2:
			engine.Stop()
			t.Fatalf("timeout waiting for 6th spawn; got %d", spawner.callCount())
		case <-time.After(30 * time.Millisecond):
			if spawner.callCount() >= 6 {
				goto drainDone
			}
		}
	}
drainDone:
	engine.Stop()

	if spawner.callCount() < 6 {
		t.Errorf("expected 6 total spawns after drain, got %d", spawner.callCount())
	}
}

// TestTriageSpawner_BinaryNotFound verifies that AgentDeckLaunchSpawner returns
// a non-nil error when the binary path does not exist.
func TestTriageSpawner_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	req := TriageRequest{
		Event:      Event{Source: "mock", Sender: "test@example.com", Subject: "test", Timestamp: time.Now()},
		WatcherID:  "w1",
		Profile:    "test",
		TriageDir:  dir,
		ResultPath: dir + "/result.json",
		SpawnedAt:  time.Now(),
	}

	spawner := AgentDeckLaunchSpawner{BinaryPath: "/definitely/not/a/real/path/agent-deck"}
	_, err := spawner.Spawn(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-existent binary, got nil")
	}
	// The error should mention something about the path being invalid.
	t.Logf("got expected error: %v", err)
}
