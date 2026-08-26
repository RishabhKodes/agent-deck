package watcher_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
	"github.com/RishabhKodes/agent-deck/internal/watcher"
)

// TestEventDedupKey_SameInputsSameKey verifies that identical inputs produce the same DedupKey.
func TestEventDedupKey_SameInputsSameKey(t *testing.T) {
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	e1 := watcher.Event{
		Source:    "webhook",
		Sender:    "user@example.com",
		Subject:   "New issue",
		Timestamp: ts,
	}
	e2 := watcher.Event{
		Source:    "webhook",
		Sender:    "user@example.com",
		Subject:   "New issue",
		Timestamp: ts,
	}
	if e1.DedupKey() != e2.DedupKey() {
		t.Errorf("expected same DedupKey for identical events, got %q and %q", e1.DedupKey(), e2.DedupKey())
	}
}

// TestEventDedupKey_DifferentSenderDifferentKey verifies that different senders produce different keys.
func TestEventDedupKey_DifferentSenderDifferentKey(t *testing.T) {
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	e1 := watcher.Event{
		Source:    "webhook",
		Sender:    "user1@example.com",
		Subject:   "New issue",
		Timestamp: ts,
	}
	e2 := watcher.Event{
		Source:    "webhook",
		Sender:    "user2@example.com",
		Subject:   "New issue",
		Timestamp: ts,
	}
	if e1.DedupKey() == e2.DedupKey() {
		t.Errorf("expected different DedupKeys for different senders, but got same key %q", e1.DedupKey())
	}
}

// TestEventJSONRoundTrip verifies that Event marshals and unmarshals without data loss.
func TestEventJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	rawPayload := json.RawMessage(`{"key":"value","num":42}`)
	orig := watcher.Event{
		Source:     "ntfy",
		Sender:     "bot@example.com",
		Subject:    "Alert",
		Body:       "Something happened",
		Timestamp:  ts,
		RawPayload: rawPayload,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("failed to marshal Event: %v", err)
	}
	var decoded watcher.Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal Event: %v", err)
	}
	if decoded.Source != orig.Source {
		t.Errorf("Source mismatch: got %q, want %q", decoded.Source, orig.Source)
	}
	if decoded.Sender != orig.Sender {
		t.Errorf("Sender mismatch: got %q, want %q", decoded.Sender, orig.Sender)
	}
	if decoded.Subject != orig.Subject {
		t.Errorf("Subject mismatch: got %q, want %q", decoded.Subject, orig.Subject)
	}
	if decoded.Body != orig.Body {
		t.Errorf("Body mismatch: got %q, want %q", decoded.Body, orig.Body)
	}
	if !decoded.Timestamp.Equal(orig.Timestamp) {
		t.Errorf("Timestamp mismatch: got %v, want %v", decoded.Timestamp, orig.Timestamp)
	}
	if string(decoded.RawPayload) != string(orig.RawPayload) {
		t.Errorf("RawPayload mismatch: got %q, want %q", string(decoded.RawPayload), string(orig.RawPayload))
	}
}

// TestEventDedupKey_EmptyCustomDedupKey verifies backward compatibility: when
// CustomDedupKey is empty, DedupKey() returns the SHA-256 hash.
func TestEventDedupKey_EmptyCustomDedupKey(t *testing.T) {
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	e := watcher.Event{
		Source:    "webhook",
		Sender:    "user@example.com",
		Subject:   "New issue",
		Timestamp: ts,
	}
	key := e.DedupKey()
	// SHA-256 hex is 64 characters
	if len(key) != 64 {
		t.Errorf("expected 64-char SHA-256 hex, got len=%d: %q", len(key), key)
	}
}

// TestEventDedupKey_CustomDedupKeyOverride verifies that when CustomDedupKey is
// set, DedupKey() returns that exact value instead of the SHA-256 hash.
func TestEventDedupKey_CustomDedupKeyOverride(t *testing.T) {
	e := watcher.Event{
		Source:         "slack",
		Sender:         "slack:C123",
		Subject:        "Hello",
		Timestamp:      time.Now(),
		CustomDedupKey: "slack-C123-1712345678.123",
	}
	key := e.DedupKey()
	if key != "slack-C123-1712345678.123" {
		t.Errorf("expected CustomDedupKey override, got %q", key)
	}
}

// newAdapterTestDB creates a temporary StateDB for adapter/statedb integration tests.
func newAdapterTestDB(t *testing.T) *statedb.StateDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := statedb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertTestWatcher inserts a watcher row for statedb tests.
func insertTestWatcher(t *testing.T, db *statedb.StateDB, id, name string) {
	t.Helper()
	now := time.Now()
	err := db.SaveWatcher(&statedb.WatcherRow{
		ID:        id,
		Name:      name,
		Type:      "test",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("SaveWatcher: %v", err)
	}
}

// TestLookupWatcherEventSession_ExistsWithSessionID verifies that
// LookupWatcherEventSessionByDedupKey returns the session_id when
// an event exists with a non-empty session_id.
func TestLookupWatcherEventSession_ExistsWithSessionID(t *testing.T) {
	db := newAdapterTestDB(t)
	insertTestWatcher(t, db, "w1", "test-watcher")

	// Insert an event with a known session_id via raw SQL
	_, err := db.DB().Exec(
		`INSERT INTO watcher_events (watcher_id, dedup_key, sender, subject, routed_to, session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"w1", "slack-C123-1712345678.123", "slack:C123", "test", "conductor-1", "sess-abc", time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	sid, err := db.LookupWatcherEventSessionByDedupKey("w1", "slack-C123-1712345678.123")
	if err != nil {
		t.Fatalf("LookupWatcherEventSessionByDedupKey: %v", err)
	}
	if sid != "sess-abc" {
		t.Errorf("expected session_id=sess-abc, got %q", sid)
	}
}

// TestLookupWatcherEventSession_ExistsEmptySessionID verifies that
// LookupWatcherEventSessionByDedupKey returns empty string when
// event exists but session_id is empty.
func TestLookupWatcherEventSession_ExistsEmptySessionID(t *testing.T) {
	db := newAdapterTestDB(t)
	insertTestWatcher(t, db, "w1", "test-watcher")

	_, err := db.DB().Exec(
		`INSERT INTO watcher_events (watcher_id, dedup_key, sender, subject, routed_to, session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"w1", "slack-C123-1712345679.456", "slack:C123", "test", "", "", time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	sid, err := db.LookupWatcherEventSessionByDedupKey("w1", "slack-C123-1712345679.456")
	if err != nil {
		t.Fatalf("LookupWatcherEventSessionByDedupKey: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session_id, got %q", sid)
	}
}

// TestLookupWatcherEventSession_NotFound verifies that
// LookupWatcherEventSessionByDedupKey returns ("", nil) when no matching event.
func TestLookupWatcherEventSession_NotFound(t *testing.T) {
	db := newAdapterTestDB(t)

	sid, err := db.LookupWatcherEventSessionByDedupKey("w-nonexistent", "nonexistent-key")
	if err != nil {
		t.Fatalf("expected nil error for missing event, got %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session_id for missing event, got %q", sid)
	}
}

// TestUpdateWatcherEventSessionID_Success verifies that
// UpdateWatcherEventSessionID updates the session_id column.
func TestUpdateWatcherEventSessionID_Success(t *testing.T) {
	db := newAdapterTestDB(t)
	insertTestWatcher(t, db, "w1", "test-watcher")

	_, err := db.DB().Exec(
		`INSERT INTO watcher_events (watcher_id, dedup_key, sender, subject, routed_to, session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"w1", "slack-C123-1712345680.789", "slack:C123", "test", "", "", time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	err = db.UpdateWatcherEventSessionID("w1", "slack-C123-1712345680.789", "sess-new")
	if err != nil {
		t.Fatalf("UpdateWatcherEventSessionID: %v", err)
	}

	// Verify the update
	sid, err := db.LookupWatcherEventSessionByDedupKey("w1", "slack-C123-1712345680.789")
	if err != nil {
		t.Fatalf("lookup after update: %v", err)
	}
	if sid != "sess-new" {
		t.Errorf("expected session_id=sess-new after update, got %q", sid)
	}
}

// TestUpdateWatcherEventSessionID_NoMatch verifies that
// UpdateWatcherEventSessionID returns an error when no row matches.
func TestUpdateWatcherEventSessionID_NoMatch(t *testing.T) {
	db := newAdapterTestDB(t)

	err := db.UpdateWatcherEventSessionID("w-nonexistent", "no-such-key", "sess-123")
	if err == nil {
		t.Fatal("expected error for non-matching watcher_id+dedup_key, got nil")
	}
}
