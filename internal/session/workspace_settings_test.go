package session

import "testing"

func TestWorkspaceSettingsSidebarWidth(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, DefaultWorkspaceSidebarWidth},
		{10, MinWorkspaceSidebarWidth},
		{32, 32},
		{80, MaxWorkspaceSidebarWidth},
	}
	for _, tt := range tests {
		if got := (WorkspaceSettings{SidebarWidth: tt.input}).GetSidebarWidth(); got != tt.want {
			t.Errorf("sidebar_width %d => %d, want %d", tt.input, got, tt.want)
		}
	}
}
