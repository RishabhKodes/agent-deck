package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func makeTestInstances() []*session.Instance {
	return []*session.Instance{
		{ID: "id-1", Title: "frontend-agent", Tool: "claude", Status: session.StatusRunning},
		{ID: "id-2", Title: "backend-agent", Tool: "claude", Status: session.StatusWaiting},
		{ID: "id-3", Title: "data-pipeline", Tool: "gemini", Status: session.StatusIdle},
	}
}

func TestNewSessionPickerDialog(t *testing.T) {
	d := NewSessionPickerDialog()
	if d.IsVisible() {
		t.Error("new dialog should not be visible")
	}
	if d.GetSelected() != nil {
		t.Error("new dialog should have nil selected")
	}
	if d.GetSource() != nil {
		t.Error("new dialog should have nil source")
	}
}

func TestShow_FiltersSourceSession(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()

	d.Show(instances[0], instances)

	if !d.IsVisible() {
		t.Error("dialog should be visible after Show")
	}
	if len(d.sessions) != 2 {
		t.Errorf("expected 2 filtered sessions, got %d", len(d.sessions))
	}
	// Source should be excluded
	for _, s := range d.sessions {
		if s.ID == "id-1" {
			t.Error("source session should be excluded")
		}
	}
}

func TestShow_FiltersErrorSessions(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := []*session.Instance{
		{ID: "id-1", Title: "source", Tool: "claude", Status: session.StatusRunning},
		{ID: "id-2", Title: "good", Tool: "claude", Status: session.StatusWaiting},
		{ID: "id-3", Title: "errored", Tool: "claude", Status: session.StatusError},
	}

	d.Show(instances[0], instances)

	if len(d.sessions) != 1 {
		t.Errorf("expected 1 session (error filtered out), got %d", len(d.sessions))
	}
	if d.sessions[0].Title != "good" {
		t.Errorf("expected 'good', got '%s'", d.sessions[0].Title)
	}
}

func TestShow_FiltersStoppedSessions(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := []*session.Instance{
		{ID: "id-1", Title: "source", Tool: "claude", Status: session.StatusRunning},
		{ID: "id-2", Title: "good", Tool: "claude", Status: session.StatusWaiting},
		{ID: "id-3", Title: "stopped", Tool: "claude", Status: session.StatusStopped},
	}

	d.Show(instances[0], instances)

	if len(d.sessions) != 1 {
		t.Errorf("expected 1 session (stopped filtered out), got %d", len(d.sessions))
	}
	if d.sessions[0].Title != "good" {
		t.Errorf("expected 'good', got '%s'", d.sessions[0].Title)
	}
}

func TestShow_EmptyAfterFilter(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := []*session.Instance{
		{ID: "id-1", Title: "only-one", Tool: "claude", Status: session.StatusRunning},
	}

	d.Show(instances[0], instances)

	if len(d.sessions) != 0 {
		t.Errorf("expected 0 sessions when source is the only one, got %d", len(d.sessions))
	}
}

func TestHide_ResetsState(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances)
	d.cursor = 1

	d.Hide()

	if d.IsVisible() {
		t.Error("dialog should not be visible after Hide")
	}
	if d.cursor != 0 {
		t.Error("cursor should be reset to 0")
	}
	if d.sourceSession != nil {
		t.Error("sourceSession should be nil")
	}
	if d.sessions != nil {
		t.Error("sessions should be nil")
	}
}

func TestNavigation_Down(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances)

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if d.cursor != 1 {
		t.Errorf("expected cursor=1 after j, got %d", d.cursor)
	}
}

func TestNavigation_Up(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances)
	d.cursor = 1

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after k, got %d", d.cursor)
	}
}

func TestNavigation_WrapAroundDown(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances) // 2 sessions after filtering

	d.cursor = 1 // at the end
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after wrapping, got %d", d.cursor)
	}
}

func TestNavigation_WrapAroundUp(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances) // 2 sessions after filtering

	d.cursor = 0
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if d.cursor != 1 {
		t.Errorf("expected cursor=1 after wrapping up, got %d", d.cursor)
	}
}

func TestGetSelected_ReturnsCorrectSession(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances)
	d.cursor = 1

	selected := d.GetSelected()
	if selected == nil {
		t.Fatal("expected non-nil selected")
	}
	if selected.ID != "id-3" {
		t.Errorf("expected id-3, got %s", selected.ID)
	}
}

func TestGetSelected_NilWhenEmpty(t *testing.T) {
	d := NewSessionPickerDialog()
	d.Show(&session.Instance{ID: "only"}, []*session.Instance{{ID: "only"}})

	if d.GetSelected() != nil {
		t.Error("expected nil when no sessions available")
	}
}

func TestView_ContainsSourceTitle(t *testing.T) {
	InitTheme("dark")
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.SetSize(80, 24)
	d.Show(instances[0], instances)

	view := d.View()
	if !strings.Contains(view, "frontend-agent") {
		t.Error("view should contain source title")
	}
}

func TestView_ContainsSessionInfo(t *testing.T) {
	InitTheme("dark")
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.SetSize(80, 24)
	d.Show(instances[0], instances)

	view := d.View()
	if !strings.Contains(view, "backend-agent") {
		t.Error("view should contain target session title")
	}
	if !strings.Contains(view, "claude") {
		t.Error("view should contain tool name")
	}
}

func TestView_Centering(t *testing.T) {
	InitTheme("dark")
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.SetSize(80, 24)
	d.Show(instances[0], instances)

	view := d.View()
	lines := strings.Split(view, "\n")
	// Should have some leading empty lines for centering
	if len(lines) < 5 {
		t.Error("view should have enough lines for centering")
	}
}

func TestUpdate_EscHides(t *testing.T) {
	d := NewSessionPickerDialog()
	instances := makeTestInstances()
	d.Show(instances[0], instances)

	d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.IsVisible() {
		t.Error("Esc should hide the dialog")
	}
}
