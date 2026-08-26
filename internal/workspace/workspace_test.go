package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

func TestClampSidebarWidth(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, DefaultSidebarWidth},
		{-1, DefaultSidebarWidth},
		{10, MinSidebarWidth},
		{24, 24},
		{41, 41},
		{99, MaxSidebarWidth},
	}
	for _, tt := range tests {
		if got := ClampSidebarWidth(tt.input); got != tt.want {
			t.Errorf("ClampSidebarWidth(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestClampSidebarForTerminalPreservesAgentWidth(t *testing.T) {
	if got := clampSidebarForTerminal(60, 80); got != 39 {
		t.Fatalf("narrow terminal sidebar = %d, want 39", got)
	}
	if got := clampSidebarForTerminal(40, 120); got != 40 {
		t.Fatalf("wide terminal sidebar = %d, want 40", got)
	}
}

func TestFindAttachTargetRequiresExactStableID(t *testing.T) {
	archivedAt := time.Now()
	instances := []*session.InstanceData{
		{ID: "abc", Title: "Alpha", Tool: "codex", TmuxSession: "agentdeck_alpha_1", TmuxSocketName: "inner-a"},
		{ID: "abcd", Title: "Beta", Tool: "claude", TmuxSession: "agentdeck_beta_2", ArchivedAt: archivedAt},
	}

	if _, ok := FindAttachTarget(instances, "ab"); ok {
		t.Fatal("prefix lookup must not resolve an attach target")
	}
	if _, ok := FindAttachTarget(instances, "Alpha"); ok {
		t.Fatal("title lookup must not resolve an attach target")
	}
	got, ok := FindAttachTarget(instances, "abcd")
	if !ok {
		t.Fatal("exact ID did not resolve")
	}
	if got.TmuxName != "agentdeck_beta_2" || !got.Archived {
		t.Fatalf("resolved target = %#v", got)
	}
}

func TestWithoutEnvScrubsOuterTmuxIdentity(t *testing.T) {
	got := withoutEnv([]string{"PATH=/bin", "TMUX=/tmp/outer,1,0", "TMUX_PANE=%4", "TERM=tmux-256color"}, "TMUX", "TMUX_PANE")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "TMUX=") || strings.Contains(joined, "TMUX_PANE=") {
		t.Fatalf("outer tmux identity leaked: %v", got)
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "TERM=tmux-256color") {
		t.Fatalf("unrelated environment was removed: %v", got)
	}
}

func TestDashboardSessionDoesNotUseManagedAgentPrefix(t *testing.T) {
	testHome := t.TempDir()
	t.Setenv("HOME", testHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(testHome, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(testHome, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(testHome, "cache"))
	id := identityForProfile("default")
	if strings.HasPrefix(id.session, "agentdeck_") {
		t.Fatalf("outer session %q would be discovered as an agent session", id.session)
	}
	if id.owner == "" || !strings.Contains(id.owner, ":v1:") {
		t.Fatalf("missing versioned owner marker: %#v", id)
	}
}

func TestShellCommandQuotesEveryArgument(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "injected")
	malicious := "id'; touch " + marker + "; echo '"
	command := shellCommand("/usr/bin/printf", "%s", malicious)
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatalf("run quoted command: %v", err)
	}
	if string(out) != malicious {
		t.Fatalf("argument changed while quoting: %q != %q", out, malicious)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell injection created %s", marker)
	}
}
