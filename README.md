# Agent Deck

Agent Deck is a lightweight local terminal dashboard for running AI coding
sessions side by side. It stores session metadata locally, keeps each agent in
its own tmux session, and lets you switch, prompt, inspect output, and manage
worktrees without leaving the dashboard.

## Install

```bash
git clone https://github.com/RishabhKodes/agent-deck.git
cd agent-deck
make install
```

Run `agent-deck` to open the dashboard. The fork has no web server, updater,
remote SSH controller, fleet/conductor service, or telemetry dependency.

## Supported agents

Built-in tool profiles cover Claude, Codex, Gemini, OpenCode, Pi, Copilot,
Cursor, Hermes, DeepSeek, Crush, Aider, and a regular shell. Custom commands
can be configured when a built-in profile is not appropriate.

## Common commands

```bash
agent-deck add claude .                 # register a local session
agent-deck launch . -c codex -m "Review this project"
agent-deck list
agent-deck status
agent-deck session restart my-project
agent-deck session send my-project "Please continue"
agent-deck session fork my-project
agent-deck mcp attach my-project exa
agent-deck skill attach my-project docs --restart
agent-deck plugin attach my-project my-plugin --restart
agent-deck group list
agent-deck worktree list
agent-deck try prototype       # create/find a dated experiment session
agent-deck costs summary       # inspect recorded Claude usage
agent-deck profile list
```

Use `agent-deck help` or any command's `--help` flag for the complete command
reference. Select a profile with `-p/--profile` and a group with
`-g/--group`. Hook integrations are available through `hooks`, `codex-hooks`,
`gemini-hooks`, `hermes-hooks`, and `cursor-hooks`.

## Dashboard shortcuts

`j`/`k` or arrows move through sessions. `Enter` opens the selected session's
Output pane; type and press `Enter` to submit, or `Esc` to return to the list.
`n` creates a session, `m` manages MCPs, `s` manages skills, `f` forks, `r`
renames, `R` restarts, `d` removes, `/` searches, `$` opens cost tracking, and
`?` shows the in-app help.

## Configuration and data

Configuration lives at `$XDG_CONFIG_HOME/agent-deck/config.toml` (normally
`~/.config/agent-deck/config.toml`). Session state is stored under the XDG data
directory. Existing databases and configuration files remain readable; legacy
remote/orchestration records are retained as inert data and are not shown in
the local dashboard.

See the maintained references in
[`skills/agent-deck/references/`](skills/agent-deck/references/) for setup,
configuration, troubleshooting, goals, and TUI details.

## License

MIT. See [LICENSE](LICENSE).
