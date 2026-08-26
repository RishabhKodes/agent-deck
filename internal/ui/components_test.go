package ui

import (
	"testing"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// Tree tests
func TestNewTree(t *testing.T) {
	tree := NewTree()
	if tree == nil {
		t.Fatal("NewTree returned nil")
	}
}

func TestTreeAddFolder(t *testing.T) {
	tree := NewTree()
	tree.AddFolder("projects")
	tree.AddFolder("personal")

	folders := tree.GetFolders()
	if len(folders) != 2 {
		t.Errorf("Expected 2 folders, got %d", len(folders))
	}
}

func TestTreeToggleFolder(t *testing.T) {
	tree := NewTree()
	tree.AddFolder("test")

	// Default should be expanded
	if !tree.IsFolderExpanded("test") {
		t.Error("Folder should be expanded by default")
	}

	tree.ToggleFolder("test")
	if tree.IsFolderExpanded("test") {
		t.Error("Folder should be collapsed after toggle")
	}
}

// List tests
func TestNewList(t *testing.T) {
	list := NewList()
	if list == nil {
		t.Fatal("NewList returned nil")
	}
}

func TestListSetItems(t *testing.T) {
	list := NewList()
	items := []*session.Instance{
		{Title: "session-1", ProjectPath: "/tmp/1"},
		{Title: "session-2", ProjectPath: "/tmp/2"},
	}
	list.SetItems(items)

	if list.Len() != 2 {
		t.Errorf("Expected 2 items, got %d", list.Len())
	}
}

func TestListNavigation(t *testing.T) {
	list := NewList()
	items := []*session.Instance{
		{Title: "session-1"},
		{Title: "session-2"},
		{Title: "session-3"},
	}
	list.SetItems(items)

	// Start at 0
	if list.Cursor() != 0 {
		t.Errorf("Cursor should start at 0, got %d", list.Cursor())
	}

	// Move down
	list.MoveDown()
	if list.Cursor() != 1 {
		t.Errorf("Cursor should be 1 after MoveDown, got %d", list.Cursor())
	}

	// Move up
	list.MoveUp()
	if list.Cursor() != 0 {
		t.Errorf("Cursor should be 0 after MoveUp, got %d", list.Cursor())
	}
}

func TestListSelected(t *testing.T) {
	list := NewList()
	items := []*session.Instance{
		{Title: "session-1"},
		{Title: "session-2"},
	}
	list.SetItems(items)

	selected := list.Selected()
	if selected == nil {
		t.Fatal("Selected should not be nil")
	}
	if selected.Title != "session-1" {
		t.Errorf("Expected session-1, got %s", selected.Title)
	}
}

// Preview tests
func TestNewPreview(t *testing.T) {
	preview := NewPreview()
	if preview == nil {
		t.Fatal("NewPreview returned nil")
	}
}

func TestPreviewSetContent(t *testing.T) {
	preview := NewPreview()
	preview.SetContent("Test content", "test-session")

	view := preview.View()
	if view == "" {
		t.Error("Preview view should not be empty")
	}
}

func TestPreviewSetSize(t *testing.T) {
	preview := NewPreview()
	preview.SetSize(80, 24)

	if preview.width != 80 {
		t.Errorf("Width = %d, want 80", preview.width)
	}
	if preview.height != 24 {
		t.Errorf("Height = %d, want 24", preview.height)
	}
}

// Menu tests
func TestNewMenu(t *testing.T) {
	menu := NewMenu()
	if menu == nil {
		t.Fatal("NewMenu returned nil")
	}
}

func TestMenuView(t *testing.T) {
	menu := NewMenu()
	menu.SetWidth(80)

	view := menu.View()
	if view == "" {
		t.Error("Menu view should not be empty")
	}
}
