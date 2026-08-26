package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/statedb"
	"github.com/stretchr/testify/require"
)

func newTestDB(t *testing.T) *statedb.StateDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := statedb.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, db.Migrate())
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewStorageWatcher(t *testing.T) {
	db := newTestDB(t)
	watcher, err := NewStorageWatcher(db)
	require.NoError(t, err)
	require.NotNil(t, watcher)
	defer watcher.Close()
}

func TestStorageWatcher_DetectsChanges(t *testing.T) {
	db := newTestDB(t)
	watcher, err := NewStorageWatcher(db)
	require.NoError(t, err)
	defer watcher.Close()

	watcher.Start()

	// Simulate an external change (another instance touching the metadata)
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, db.Touch())

	// Should receive reload signal within the poll interval
	select {
	case <-watcher.ReloadChannel():
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Expected reload signal but got timeout")
	}
}

func TestStorageWatcher_NotifySaveIgnoresOwnChanges(t *testing.T) {
	db := newTestDB(t)
	watcher, err := NewStorageWatcher(db)
	require.NoError(t, err)
	defer watcher.Close()

	watcher.Start()

	// Notify that we're about to save (simulating TUI save)
	watcher.NotifySave()

	// Touch metadata (this simulates TUI's own save via storage.SaveWithGroups)
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, db.Touch())

	// Should NOT receive reload signal (within ignore window)
	select {
	case <-watcher.ReloadChannel():
		t.Fatal("Should not receive reload signal for TUI's own save")
	case <-time.After(3 * time.Second):
		// Success: no reload signal received
	}
}

func TestStorageWatcher_ExternalChangesStillDetected(t *testing.T) {
	db := newTestDB(t)
	watcher, err := NewStorageWatcher(db)
	require.NoError(t, err)
	defer watcher.Close()

	watcher.Start()

	// Notify that we saved
	watcher.NotifySave()

	// Wait for ignore window to expire (ignoreWindow is 3s)
	time.Sleep(4 * time.Second)

	// Now an external change should be detected
	require.NoError(t, db.Touch())

	// Should receive reload signal (outside ignore window)
	select {
	case <-watcher.ReloadChannel():
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Expected reload signal for external change but got timeout")
	}
}

// TestStorageWatcher_CrossProfileIsolation verifies that separate SQLite databases
// for different profiles are naturally isolated (each has its own metadata).
func TestStorageWatcher_CrossProfileIsolation(t *testing.T) {
	db1 := newTestDB(t)
	db2 := newTestDB(t)

	// Create watcher for db1 only
	watcher1, err := NewStorageWatcher(db1)
	require.NoError(t, err)
	defer watcher1.Close()
	watcher1.Start()

	// Touch db2's metadata (simulating another profile saving)
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, db2.Touch())

	// Watcher1 should NOT fire (it's watching a different database)
	select {
	case <-watcher1.ReloadChannel():
		t.Fatal("CRITICAL BUG: Watcher1 fired when db2 was modified!")
	case <-time.After(3 * time.Second):
		// Success: isolated correctly
	}

	// Watcher1 SHOULD fire when its own database changes
	require.NoError(t, db1.Touch())

	select {
	case <-watcher1.ReloadChannel():
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Watcher1 should have detected change to its own database")
	}
}

func TestStorageWatcher_NilDB(t *testing.T) {
	watcher, err := NewStorageWatcher(nil)
	require.NoError(t, err)
	require.Nil(t, watcher)
}
