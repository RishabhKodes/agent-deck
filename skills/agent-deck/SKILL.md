---
name: agent-deck
description: Use Agent Deck to manage local terminal sessions for AI coding agents.
---

# Agent Deck

Agent Deck is a local tmux session manager. Use it to register, launch, inspect,
and organize coding-agent sessions on the current machine.

## Common commands

```sh
agent-deck                 # open the dashboard
agent-deck add [path]      # register a local session
agent-deck launch [path]   # register and start a session
agent-deck list             # list sessions
agent-deck status           # inspect status
agent-deck session <cmd>    # start, stop, restart, remove, send
agent-deck group <cmd>      # manage groups
agent-deck worktree <cmd>   # manage git worktree sessions
```

Use `-p, --profile NAME` to select a profile and `-g, --group NAME` to place a
session in a group. Run any command with `--help` for the complete local CLI.

## Session workflow

1. Register a project with `agent-deck add /path/to/project`.
2. Start it with `agent-deck session start <id>` or use `launch` to do both.
3. Attach with `agent-deck session attach <id>` or open the dashboard.
4. Send a prompt with `agent-deck session send <id> "..."`.
5. Stop, restart, archive, or remove sessions through `agent-deck session`.

Sessions, groups, profiles, MCP attachments, and skills are persisted locally
under the Agent Deck data directory. Existing legacy records remain readable;
unsupported remote or orchestration records are ignored by the local views.

## MCP and skills

Use `agent-deck mcp` to inspect or attach MCP servers and `agent-deck skill` to
list, attach, detach, or inspect project skills. These operations update only
the selected local session.

## Worktrees

Use `agent-deck worktree create` when a change needs an isolated git worktree,
then use the corresponding session commands to start and inspect it. Keep the
base repository clean and review worktree status before removing one.

## Safety

- Keep all paths explicit and local.
- Confirm before removing a session or worktree.
- Preserve user changes and inspect `git diff` after automated edits.
- Do not assume a stopped session is safe to delete; inspect its transcript or
  worktree first.
