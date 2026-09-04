package session

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeLocalSkillForTest(t *testing.T, root, dir, name string) {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test\n---\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListLocalAgentSkillsKeepsClaudeAndCursorSeparate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	project := t.TempDir()

	writeLocalSkillForTest(t, filepath.Join(project, ".claude", "skills"), "claude-project", "claude-project")
	writeLocalSkillForTest(t, filepath.Join(home, ".claude", "skills"), "claude-user", "claude-user")
	writeLocalSkillForTest(t, filepath.Join(project, ".cursor", "skills"), "cursor-project", "cursor-project")
	writeLocalSkillForTest(t, filepath.Join(home, ".cursor", "skills"), "cursor-user", "cursor-user")

	claude := ListLocalAgentSkills(project, "claude", filepath.Join(home, ".claude"))
	cursor := ListLocalAgentSkills(project, "cursor", filepath.Join(home, ".cursor"))
	if !slices.Equal(claude, []string{"claude-project", "claude-user"}) {
		t.Fatalf("Claude skills = %v", claude)
	}
	if !slices.Equal(cursor, []string{"cursor-project", "cursor-user"}) {
		t.Fatalf("Cursor skills = %v", cursor)
	}
}

func TestListLocalAgentSkillsFollowsManagedDirectorySymlink(t *testing.T) {
	project := t.TempDir()
	source := t.TempDir()
	writeLocalSkillForTest(t, source, "linked", "linked")
	targetRoot := filepath.Join(project, ".cursor", "skills")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "linked"), filepath.Join(targetRoot, "linked")); err != nil {
		t.Fatal(err)
	}

	got := ListLocalAgentSkills(project, "cursor", "")
	if !slices.Contains(got, "linked") {
		t.Fatalf("skills = %v, want linked symlink target", got)
	}
}
