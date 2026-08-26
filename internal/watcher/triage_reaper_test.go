package watcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
)

// captureHandler is a slog.Handler that records log records for test assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(name string) slog.Handler       { return h }

func (h *captureHandler) hasMsg(substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if strings.Contains(r.Message, substr) {
			return true
		}
		// Check attributes too.
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if strings.Contains(a.Key, substr) || strings.Contains(a.Value.String(), substr) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// insertWatcherEvent inserts a watcher_events row for reaper tests.
func insertWatcherEvent(t *testing.T, db *statedb.StateDB, watcherID, dedupKey, sender, routedTo string) {
	t.Helper()
	_, err := db.DB().Exec(
		`INSERT OR IGNORE INTO watcher_events (watcher_id, dedup_key, sender, subject, routed_to, session_id, triage_session_id, created_at)
		 VALUES (?, ?, ?, 'test subject', ?, '', '', ?)`,
		watcherID, dedupKey, sender, routedTo, time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insert watcher_event: %v", err)
	}
}

// writeResultJSON writes a triageResult as JSON to the given path.
func writeResultJSON(t *testing.T, path string, result triageResult) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write result.json: %v", err)
	}
}

// queryRoutedTo returns the routed_to value for the given watcherID + dedupKey.
func queryRoutedTo(t *testing.T, db *statedb.StateDB, watcherID, dedupKey string) string {
	t.Helper()
	var routedTo string
	err := db.DB().QueryRow(
		`SELECT routed_to FROM watcher_events WHERE watcher_id = ? AND dedup_key = ?`,
		watcherID, dedupKey,
	).Scan(&routedTo)
	if err != nil {
		t.Fatalf("query routed_to: %v", err)
	}
	return routedTo
}

// TestTriageReaper_ProcessesHighConfidence verifies high+persist: AppendClientEntry called,
// Router reloaded, routed_to updated, result.json renamed to result.processed.json.
func TestTriageReaper_ProcessesHighConfidence(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	router := NewRouter(nil)
	clock := newFakeClock()

	// Pre-insert a watcher and an event with routedTo="triage".
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")

	// Create the triage dir for the dedup key.
	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	// Use a known dedup key.
	dedupKey := "highconfidencededupkey001"
	insertWatcherEvent(t, db, "w1", dedupKey, "new@clienta.com", "triage")

	// Write result.json in the triage dir.
	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	processedPath := filepath.Join(hashDirPath, "result.processed.json")

	writeResultJSON(t, resultPath, triageResult{
		RouteTo:       "client-a",
		Group:         "client-a/inbox",
		Name:          "Client A",
		Sender:        "new@clienta.com",
		Summary:       "New contact",
		Confidence:    "high",
		ShouldPersist: true,
	})

	r.scanOnce()

	// (a) routed_to updated in DB.
	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "client-a" {
		t.Errorf("expected routed_to=client-a, got %q", routedTo)
	}

	// (b) clients.json has the new entry.
	data, err := os.ReadFile(clientsPath)
	if err != nil {
		t.Fatalf("read clients.json: %v", err)
	}
	var clients map[string]ClientEntry
	if err := json.Unmarshal(data, &clients); err != nil {
		t.Fatalf("parse clients.json: %v", err)
	}
	if _, ok := clients["new@clienta.com"]; !ok {
		t.Errorf("expected new@clienta.com in clients.json, got keys: %v", clients)
	}

	// (c) Router.Match now returns the new route.
	match := router.Match("new@clienta.com")
	if match == nil {
		t.Fatal("expected router to match new@clienta.com after reload")
	}
	if match.Conductor != "client-a" {
		t.Errorf("expected conductor=client-a, got %q", match.Conductor)
	}

	// (d) result.processed.json exists, result.json does not.
	if _, err := os.Stat(processedPath); err != nil {
		t.Errorf("expected result.processed.json to exist: %v", err)
	}
	if _, err := os.Stat(resultPath); err == nil {
		t.Error("expected result.json to be gone after rename")
	}
}

// TestTriageReaper_MediumNoPersist verifies medium confidence: no persist, no router reload,
// result renamed, and the TODO-D13-NOTIFY log line is emitted.
func TestTriageReaper_MediumNoPersist(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	// Write an empty clients.json so we can verify it's unchanged.
	if err := os.WriteFile(clientsPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write clients.json: %v", err)
	}
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "mediumconfidencededupkey001"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "maybe@example.com", "triage")

	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	processedPath := filepath.Join(hashDirPath, "result.processed.json")

	writeResultJSON(t, resultPath, triageResult{
		RouteTo:       "client-b",
		Group:         "client-b/inbox",
		Name:          "Client B",
		Sender:        "maybe@example.com",
		Summary:       "Maybe",
		Confidence:    "medium",
		ShouldPersist: false,
	})

	r.scanOnce()

	// clients.json unchanged.
	data, err := os.ReadFile(clientsPath)
	if err != nil {
		t.Fatalf("read clients.json: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("clients.json should be unchanged for medium confidence, got %s", data)
	}

	// Router unchanged.
	if router.Match("maybe@example.com") != nil {
		t.Error("expected router to NOT match medium-confidence sender")
	}

	// routed_to set to "triage-medium".
	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "triage-medium" {
		t.Errorf("expected routed_to=triage-medium, got %q", routedTo)
	}

	// result.processed.json exists.
	if _, err := os.Stat(processedPath); err != nil {
		t.Errorf("expected result.processed.json to exist: %v", err)
	}

	// TODO-D13-NOTIFY log line emitted.
	if !handler.hasMsg("TODO-D13-NOTIFY") {
		t.Error("expected TODO-D13-NOTIFY in log output for medium confidence")
	}
}

// TestTriageReaper_LowConfidenceLogOnly verifies low confidence: no persist, routed_to=triage-low-confidence.
func TestTriageReaper_LowConfidenceLogOnly(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	if err := os.WriteFile(clientsPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write clients.json: %v", err)
	}
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "lowconfidencededupkey001"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "uncertain@example.com", "triage")

	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	writeResultJSON(t, resultPath, triageResult{
		RouteTo:       "",
		Sender:        "uncertain@example.com",
		Summary:       "Not sure",
		Confidence:    "low",
		ShouldPersist: false,
	})

	r.scanOnce()

	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "triage-low-confidence" {
		t.Errorf("expected routed_to=triage-low-confidence, got %q", routedTo)
	}
	// No clients.json write.
	data, _ := os.ReadFile(clientsPath)
	if string(data) != "{}" {
		t.Errorf("clients.json should be unchanged for low confidence")
	}
}

// TestTriageReaper_DowngradeWildcards verifies that wildcard route_to is downgraded to medium.
func TestTriageReaper_DowngradeWildcards(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	if err := os.WriteFile(clientsPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write clients.json: %v", err)
	}
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "wildcarddowngradededupkey01"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "wild@evil.com", "triage")

	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	writeResultJSON(t, resultPath, triageResult{
		RouteTo:       "*@evil.com",
		Group:         "evil/inbox",
		Name:          "Evil",
		Sender:        "wild@evil.com",
		Summary:       "Wildcard route",
		Confidence:    "high", // high but wildcard → downgrade to medium
		ShouldPersist: true,
	})

	r.scanOnce()

	// No clients.json write (downgraded to medium).
	data, _ := os.ReadFile(clientsPath)
	if string(data) != "{}" {
		t.Errorf("clients.json should be unchanged for wildcard downgrade, got: %s", data)
	}

	// routed_to set to wildcard-downgraded marker.
	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "triage-medium-wildcard-downgraded" {
		t.Errorf("expected routed_to=triage-medium-wildcard-downgraded, got %q", routedTo)
	}

	// Log line contains "wildcard downgraded".
	if !handler.hasMsg("wildcard") {
		t.Error("expected wildcard-related log message")
	}
}

// TestTriageReaper_Timeout verifies that a birth entry older than TriageTimeout gets
// routed_to set to "triage-timeout" and the birth entry is removed.
func TestTriageReaper_Timeout(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "timeoutdedupkey00000000001"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "slow@example.com", "triage")

	// Register birth at fakeClock.Now().
	r.registerBirth(dedupKey, "w1")

	// Create the hash dir so scanOnce can find it.
	hashDirPath := filepath.Join(triageDir, dedupKey)
	if err := os.MkdirAll(hashDirPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Advance clock past TriageTimeout (10 min).
	clock.Advance(11 * time.Minute)

	r.scanOnce()

	// routed_to should be triage-timeout.
	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "triage-timeout" {
		t.Errorf("expected routed_to=triage-timeout, got %q", routedTo)
	}

	// Birth entry removed.
	r.birthMu.Lock()
	_, stillPresent := r.birth[dedupKey]
	r.birthMu.Unlock()
	if stillPresent {
		t.Error("expected birth entry to be removed after timeout")
	}
}

// TestTriageReaper_ResultFileRenamed verifies that a second scanOnce does NOT
// re-process an already-processed result.
func TestTriageReaper_ResultFileRenamed(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "renamedresultdedupkey00001"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "once@example.com", "triage")

	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	processedPath := filepath.Join(hashDirPath, "result.processed.json")

	writeResultJSON(t, resultPath, triageResult{
		RouteTo:       "client-c",
		Group:         "client-c/inbox",
		Name:          "Client C",
		Sender:        "once@example.com",
		Summary:       "Once",
		Confidence:    "low",
		ShouldPersist: false,
	})

	// First scanOnce processes the result.
	r.scanOnce()
	if _, err := os.Stat(processedPath); err != nil {
		t.Fatalf("result.processed.json should exist after first scan: %v", err)
	}

	// Record initial routed_to.
	routedTo1 := queryRoutedTo(t, db, "w1", dedupKey)

	// Second scanOnce should skip (processed already).
	r.scanOnce()
	routedTo2 := queryRoutedTo(t, db, "w1", dedupKey)

	if routedTo1 != routedTo2 {
		t.Errorf("second scanOnce should not re-process; routedTo changed from %q to %q", routedTo1, routedTo2)
	}
}

// TestTriageReaper_MalformedJSON verifies that garbage result.json does not crash,
// routed_to is set to "triage-parse-error", and file is renamed.
func TestTriageReaper_MalformedJSON(t *testing.T) {
	db := newTestDB(t)
	clientsPath := filepath.Join(t.TempDir(), "clients.json")
	if err := os.WriteFile(clientsPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write clients.json: %v", err)
	}
	router := NewRouter(nil)
	clock := newFakeClock()

	triageDir := t.TempDir()
	handler := &captureHandler{}
	log := slog.New(handler)
	var wg sync.WaitGroup
	ctx := context.Background()
	r := newTriageReaper(ctx, &wg, clock, triageDir, clientsPath, router, db, log)

	dedupKey := "malformedjsondedupkey0001"
	saveTestWatcher(t, db, "w1", "test-watcher", "mock")
	insertWatcherEvent(t, db, "w1", dedupKey, "bad@example.com", "triage")

	hashDirPath := filepath.Join(triageDir, dedupKey)
	resultPath := filepath.Join(hashDirPath, "result.json")
	processedPath := filepath.Join(hashDirPath, "result.processed.json")

	if err := os.MkdirAll(hashDirPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(resultPath, []byte("{garbage bytes!!!"), 0o600); err != nil {
		t.Fatalf("write bad json: %v", err)
	}

	// Must not panic.
	r.scanOnce()

	// routed_to set to parse-error.
	routedTo := queryRoutedTo(t, db, "w1", dedupKey)
	if routedTo != "triage-parse-error" {
		t.Errorf("expected routed_to=triage-parse-error, got %q", routedTo)
	}

	// File renamed.
	if _, err := os.Stat(processedPath); err != nil {
		t.Errorf("expected result.processed.json to exist after malformed parse: %v", err)
	}
	if _, err := os.Stat(resultPath); err == nil {
		t.Error("expected result.json to be gone after rename")
	}

	// clients.json unchanged.
	data, _ := os.ReadFile(clientsPath)
	if string(data) != "{}" {
		t.Errorf("clients.json should be unchanged after malformed JSON")
	}
}
