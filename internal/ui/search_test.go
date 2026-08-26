package ui

import (
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func TestNewSearch(t *testing.T) {
	s := NewSearch()

	if s == nil {
		t.Fatal("NewSearch returned nil")
	}
	if s.IsVisible() {
		t.Error("Search should not be visible by default")
	}
	if s.cursor != 0 {
		t.Error("Cursor should start at 0")
	}
}

func TestSearchSetItems(t *testing.T) {
	s := NewSearch()
	items := []*session.Instance{
		{Title: "session-1", ProjectPath: "/tmp/1", Tool: "claude"},
		{Title: "session-2", ProjectPath: "/tmp/2", Tool: "shell"},
	}

	s.SetItems(items)

	if len(s.allItems) != 2 {
		t.Errorf("Expected 2 items, got %d", len(s.allItems))
	}
}

func TestSearchVisibility(t *testing.T) {
	s := NewSearch()

	s.Show()
	if !s.IsVisible() {
		t.Error("Search should be visible after Show()")
	}

	s.Hide()
	if s.IsVisible() {
		t.Error("Search should not be visible after Hide()")
	}
}

func TestSearchSelected(t *testing.T) {
	s := NewSearch()
	items := []*session.Instance{
		{Title: "session-1"},
		{Title: "session-2"},
	}
	s.SetItems(items)
	s.Show()

	selected := s.Selected()
	if selected == nil {
		t.Fatal("Selected should not be nil when items exist")
	}
	if selected.Title != "session-1" {
		t.Errorf("Expected session-1, got %s", selected.Title)
	}
}

func TestSearchSetSize(t *testing.T) {
	s := NewSearch()
	s.SetSize(100, 50)

	if s.width != 100 {
		t.Errorf("Width = %d, want 100", s.width)
	}
	if s.height != 50 {
		t.Errorf("Height = %d, want 50", s.height)
	}
}

func TestSearchView(t *testing.T) {
	s := NewSearch()

	// Not visible - should return empty
	view := s.View()
	if view != "" {
		t.Error("View should be empty when not visible")
	}

	// Visible - should return content
	s.SetSize(80, 24)
	s.Show()
	view = s.View()
	if view == "" {
		t.Error("View should not be empty when visible")
	}
}
