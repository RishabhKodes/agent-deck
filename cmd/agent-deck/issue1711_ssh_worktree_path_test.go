package main

import (
	"os"
	"testing"
)

// TestResolveSSHAddPaths covers the --ssh path-routing rule for `agent-deck
// add`. Regression for asheshgoplani/agent-deck#1711 / #1710: `add
// <remote-worktree-path> --ssh <host>` silently dropped the positional path
// on the floor instead of using it as the remote directory, so the launched
// session never cd'd into the intended remote worktree and instead ran in
// the SSH login shell's default directory.
//
// The routing rule takes the RAW positional argument, never a locally
// resolved one (session.ExpandPath + filepath.Abs describe the controller
// machine, not the remote host): a `~/x` or `./x` positional path is
// refused rather than silently misresolved, because wrapForSSH single-quotes
// SSHRemotePath before handing it to the remote shell, so a stored `~/x` or
// `$VAR/x` would reach the remote host inert (no tilde/variable expansion
// happens inside single quotes).
func TestResolveSSHAddPaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	tests := []struct {
		name                 string
		explicitPathProvided bool
		positionalPath       string
		explicitRemotePath   string
		wantRemotePath       string
		wantErr              bool
	}{
		{
			name:                 "absolute positional path with --ssh becomes the remote path",
			explicitPathProvided: true,
			positionalPath:       "/home/liam/pt-worktrees/some-feature",
			explicitRemotePath:   "",
			wantRemotePath:       "/home/liam/pt-worktrees/some-feature",
		},
		{
			name:                 "explicit --remote-path wins over positional path",
			explicitPathProvided: true,
			positionalPath:       "/home/liam/pt-worktrees/some-feature",
			explicitRemotePath:   "/home/liam/other-repo",
			wantRemotePath:       "/home/liam/other-repo",
		},
		{
			name:                 "no positional path, only --remote-path (documented pattern)",
			explicitPathProvided: false,
			positionalPath:       cwd, // matches handleAdd's cwd-fallback default
			explicitRemotePath:   "/home/liam/PointyTooling",
			wantRemotePath:       "/home/liam/PointyTooling",
		},
		{
			name:                 "neither positional path nor --remote-path given",
			explicitPathProvided: false,
			positionalPath:       cwd,
			explicitRemotePath:   "",
			wantRemotePath:       "",
		},
		{
			name:                 "tilde positional path is refused, not locally expanded",
			explicitPathProvided: true,
			positionalPath:       "~/pt-worktrees/tilde-feature",
			explicitRemotePath:   "",
			wantErr:              true,
		},
		{
			name:                 "relative positional path is refused, not resolved against local CWD",
			explicitPathProvided: true,
			positionalPath:       "./sub/rel-feature",
			explicitRemotePath:   "",
			wantErr:              true,
		},
		{
			name:                 "remote env-var positional path is refused",
			explicitPathProvided: true,
			positionalPath:       "$REMOTE_HOME/pt-worktrees/var-feature",
			explicitRemotePath:   "",
			wantErr:              true,
		},
		{
			name:                 "non-absolute positional path is refused even when explicit --remote-path is also non-absolute-shaped but present (--remote-path still wins, no validation applied to it)",
			explicitPathProvided: true,
			positionalPath:       "~/ignored",
			explicitRemotePath:   "/home/liam/other-repo",
			wantRemotePath:       "/home/liam/other-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLocal, gotRemote, err := resolveSSHAddPaths(tt.explicitPathProvided, tt.positionalPath, tt.explicitRemotePath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSSHAddPaths() expected an error refusing a non-absolute remote path, got remote=%q", gotRemote)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSSHAddPaths() error = %v", err)
			}
			if gotLocal != cwd {
				t.Fatalf("resolveSSHAddPaths() local placeholder = %q, want CWD %q (an --ssh session's local\n"+
					"placeholder path must always be CWD, never the remote path, so tmux never launches\n"+
					"into a path that only exists on the remote host)", gotLocal, cwd)
			}
			if gotRemote != tt.wantRemotePath {
				t.Fatalf("resolveSSHAddPaths() remote path = %q, want %q", gotRemote, tt.wantRemotePath)
			}
		})
	}
}
