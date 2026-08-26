package watcher_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/watcher"
)

// makeEntry returns a simple ClientEntry for testing.
func makeEntry(conductor, group, name string) watcher.ClientEntry {
	return watcher.ClientEntry{
		Conductor: conductor,
		Group:     group,
		Name:      name,
	}
}

// TestClientsWriter_AtomicAppend verifies the basic happy path:
// - starts with an empty {} clients.json
// - calls AppendClientEntry with a new sender
// - asserts final JSON contains the sender key
// - asserts file mode is 0o600
// - asserts no .tmp file remains in the directory
func TestClientsWriter_AtomicAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")

	// Start with empty JSON.
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	entry := makeEntry("client-a", "client-a/inbox", "Client A")
	if err := watcher.AppendClientEntry(path, "contact@clienta.com", entry); err != nil {
		t.Fatalf("AppendClientEntry returned error: %v", err)
	}

	// Verify file contains the new entry.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	var got map[string]watcher.ClientEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse final JSON: %v", err)
	}
	if _, ok := got["contact@clienta.com"]; !ok {
		t.Errorf("expected 'contact@clienta.com' in clients.json, got keys: %v", mapKeys(got))
	}

	// Verify file mode is 0o600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected file mode 0o600, got %04o", perm)
	}

	// Verify no .tmp file remains.
	assertNoTmpFiles(t, dir)
}

// TestClientsWriter_Idempotent verifies that calling AppendClientEntry twice
// with the same sender is a no-op on the second call.
func TestClientsWriter_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")

	entry := makeEntry("client-a", "client-a/inbox", "Client A")

	// First call: creates the entry.
	if err := watcher.AppendClientEntry(path, "user@example.com", entry); err != nil {
		t.Fatalf("first AppendClientEntry: %v", err)
	}

	// Second call with same sender: must return nil without error.
	if err := watcher.AppendClientEntry(path, "user@example.com", entry); err != nil {
		t.Errorf("second AppendClientEntry (duplicate) should return nil, got: %v", err)
	}

	// File must still contain exactly one entry.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var got map[string]watcher.ClientEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 entry after duplicate append, got %d", len(got))
	}
}

// TestClientsWriter_Create0600 verifies that AppendClientEntry creates the
// clients.json file (and parent directory) when they do not exist.
// Dir must be created at 0o700; file must be 0o600.
func TestClientsWriter_Create0600(t *testing.T) {
	base := t.TempDir()
	// Use a subdirectory that does not yet exist.
	dir := filepath.Join(base, "watchers")
	path := filepath.Join(dir, "clients.json")

	entry := makeEntry("conductor-x", "group/inbox", "X Client")
	if err := watcher.AppendClientEntry(path, "new@test.com", entry); err != nil {
		t.Fatalf("AppendClientEntry: %v", err)
	}

	// Verify the file exists and has mode 0o600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode: expected 0o600, got %04o", perm)
	}

	// Verify the parent directory has mode 0o700.
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode: expected 0o700, got %04o", perm)
	}
}

// TestClientsWriter_ConcurrentAppend spawns 10 goroutines each calling
// AppendClientEntry with a unique sender, then asserts all 10 keys are
// present in the final file (no interleaving loss). Must pass under -race.
func TestClientsWriter_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sender := fmt.Sprintf("user%d@concurrent.com", i)
		entry := makeEntry("conductor", "group/inbox", sender)
		go func(s string, e watcher.ClientEntry) {
			defer wg.Done()
			if err := watcher.AppendClientEntry(path, s, e); err != nil {
				t.Errorf("AppendClientEntry(%q): %v", s, err)
			}
		}(sender, entry)
	}
	wg.Wait()

	// All 10 keys must be present.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var got map[string]watcher.ClientEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if len(got) != n {
		t.Errorf("expected %d entries, got %d (keys: %v)", n, len(got), mapKeys(got))
	}
}

// TestClientsWriter_RejectsWildcard verifies that AppendClientEntry returns an
// error for wildcard senders ("*@domain") and does not create the file.
func TestClientsWriter_RejectsWildcard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")

	entry := makeEntry("conductor", "group/inbox", "Wildcard")
	err := watcher.AppendClientEntry(path, "*@example.com", entry)
	if err == nil {
		t.Fatal("expected error for wildcard sender, got nil")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("error message should contain 'wildcard', got: %q", err.Error())
	}

	// File must not have been created.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after wildcard rejection, but it does")
	}
}

// TestClientsWriter_CrashRecovery verifies that a stale .tmp file from a prior
// crash does not corrupt the next successful append.
// Setup: pre-create a valid clients.json and a stale clients.json.tmp with garbage.
// After AppendClientEntry: file must parse as valid JSON and no .tmp remains.
func TestClientsWriter_CrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")
	tmpPath := path + ".tmp"

	// Pre-create a valid clients.json with one entry.
	existing := map[string]watcher.ClientEntry{
		"existing@clienta.com": {Conductor: "client-a", Group: "client-a/inbox", Name: "Client A"},
	}
	existingData, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, existingData, 0o600); err != nil {
		t.Fatalf("setup clients.json: %v", err)
	}

	// Pre-create a stale .tmp file with garbage bytes.
	if err := os.WriteFile(tmpPath, []byte("GARBAGE BYTES FROM CRASHED WRITE"), 0o600); err != nil {
		t.Fatalf("setup stale .tmp: %v", err)
	}

	// Cleanup assertion: no .tmp files after the test.
	t.Cleanup(func() { assertNoTmpFiles(t, dir) })

	// Append a new entry — must succeed despite the stale .tmp.
	newEntry := makeEntry("client-b", "client-b/inbox", "Client B")
	if err := watcher.AppendClientEntry(path, "new@clientb.com", newEntry); err != nil {
		t.Fatalf("AppendClientEntry: %v", err)
	}

	// Final file must parse as valid JSON with both entries.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	var got map[string]watcher.ClientEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse final JSON: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d (keys: %v)", len(got), mapKeys(got))
	}
	if _, ok := got["existing@clienta.com"]; !ok {
		t.Error("original entry 'existing@clienta.com' should still be present")
	}
	if _, ok := got["new@clientb.com"]; !ok {
		t.Error("new entry 'new@clientb.com' should be present")
	}
}

// assertNoTmpFiles fails the test if any .tmp files remain in dir.
func assertNoTmpFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %q: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stale .tmp file remains in %q: %s", dir, e.Name())
		}
	}
}

// mapKeys returns the keys of a ClientEntry map for diagnostic output.
func mapKeys(m map[string]watcher.ClientEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
