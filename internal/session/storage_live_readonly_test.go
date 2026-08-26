package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLiveReadOnlyStorageSeesActiveWALAndRejectsWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	writer, err := NewStorageWithProfile("live-reader")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Save([]*Instance{{
		ID:          "wal-visible",
		Title:       "Live session",
		ProjectPath: home,
		Command:     "claude",
		Tool:        "claude",
		Status:      StatusWaiting,
		CreatedAt:   time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	reader, err := NewLiveReadOnlyStorageWithProfile("live-reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	instances, _, err := reader.LoadLite()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "wal-visible" {
		t.Fatalf("live reader did not observe WAL row: %#v", instances)
	}
	if _, err := reader.GetDB().DB().Exec("DELETE FROM instances"); err == nil {
		t.Fatal("live read-only connection unexpectedly allowed a write")
	}
}
