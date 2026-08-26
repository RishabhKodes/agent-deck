# Independent fork

This repository is an independent fork of
[asheshgoplani/agent-deck](https://github.com/asheshgoplani/agent-deck).

- Baseline release: `v1.15.0`
- Baseline commit: `bf50689893053c6dd33a29b21e12eb36e251d94b`
- Fork module: `github.com/RishabhKodes/agent-deck`
- Product and binary name: `Agent Deck` / `agent-deck`
- License: MIT; the original copyright and license text remain intact

The fork intentionally diverges from this fixed baseline. It has no automated
upstream synchronization and does not consume upstream binary updates.

## Distribution policy

There is currently no public binary, Homebrew, Pages, or automatic update
channel for this fork. Build and install it from source. Publishing workflows
and inherited notification integrations remain disabled until fork-owned
infrastructure is explicitly configured.

The inherited feedback Discussion integration is also disabled so the fork can
never submit data to the upstream project's Discussion node.

## Runtime compatibility

The fork deliberately retains Agent Deck's binary name, XDG paths, SQLite
schema, hook commands, environment variables, and managed tmux-session naming.
It acts as a drop-in replacement for v1.15.0 state. Do not run this fork and an
upstream Agent Deck installation side by side against the same profile.

## Native workspace

The fork adds the experimental `agent-deck workspace` command. It uses a
profile-scoped private outer tmux server as a two-pane compositor while leaving
all managed agents in their original inner tmux sessions. Switching the right
viewer detaches only a client; it never restarts or moves the agent process.

Only sessions persisted by Agent Deck are listed. Discovering or adopting
arbitrary Claude/Codex processes from unrelated terminal windows remains out of
scope.
