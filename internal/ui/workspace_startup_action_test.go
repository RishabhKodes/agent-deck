package ui

import "testing"

func TestAllowedWorkspaceStartupActions(t *testing.T) {
	for _, key := range []string{"n", "f", "F", "/", "?", "S", "$", "t"} {
		if got := allowedStartupAction(key); got != key {
			t.Errorf("allowedStartupAction(%q) = %q", key, got)
		}
	}
	for _, key := range []string{"", "d", "A", "R", "ctrl+c", "arbitrary"} {
		if got := allowedStartupAction(key); got != "" {
			t.Errorf("allowedStartupAction(%q) = %q, want empty", key, got)
		}
	}
}

func TestWorkspaceStartupActionRunsOnce(t *testing.T) {
	h := &Home{startupAction: "F"}
	cmd := h.startupActionCmd()
	if cmd == nil {
		t.Fatal("first startup action command is nil")
	}
	msg, ok := cmd().(startupActionMsg)
	if !ok || msg.key != "F" {
		t.Fatalf("startup message = %#v", msg)
	}
	if second := h.startupActionCmd(); second != nil {
		t.Fatal("startup action was emitted more than once")
	}
}
