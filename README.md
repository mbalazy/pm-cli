# pm

**pm** is a task tracker that lives in markdown files and speaks MCP. Your coding agent - Claude Code, Codex, any MCP client - reads and updates your tasks as a side effect of the conversation; you keep a kanban TUI, a web cockpit and `grep`. One Go binary, files under `~/.claude/pm/`, no cloud, no database, no account.

[![CI](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/mbalazy/pm-cli)](go.mod)
[![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

## What you get

- **Files you can read.** A task is one markdown file with YAML frontmatter next to its project's `project.yaml`. Each field has a write rule that survives many sessions: the brief overwrites, links merge, the Spec zone is rewritten in place, the Log zone only ever grows. `grep` works, `git` works, any editor is a client. [data-model.md](docs/data-model.md)
- **An MCP server.** `pm mcp` gives the agent 14 tools: context at session start, tasks, projects, journals, timeline. Stdio, so any MCP client can register it; Claude Code and Codex are the two with an install recipe below. It runs on an output budget, because a rollup paid at every session start is paid every time. [mcp.md](docs/mcp.md)
- **A board.** `pm` opens a kanban TUI with per-project tabs and vim keys: eight views, from the columns to a live worker transcript, and a launch menu that starts a Claude Code, Codex or executor session on a task and links it back. [board.md](docs/board.md)
- **A cockpit.** `pm serve` is a JSON API plus a React app. Its home screen is Today: one attention queue computed from local files - failed runs, work that landed with no acceptance, tasks waiting on a person, stuck projects. A change feed answers "what happened since yesterday evening" from pm's files, `git` and GitHub. No auth, no TLS; bind it to localhost. [cockpit.md](docs/cockpit.md)
- **Solo: one session, a queue, nobody watching.** The cockpit's Runs screen starts a Claude Code background session (`claude --bg`, Claude Code 2.1.272 or newer) in the project's checkout with the `/solo` skill and the queue you typed; you can `claude attach` to it from any terminal. pm reads the shift's state file and report back from `.shift/`, lists the shift under `/runs`, shows the report at `/solo/<project>/<shift>` and keeps a closed shift on Today for 14 days. The skill itself - the per-task procedure and its guard hook - lives in [mbalazy/claude-skills](https://github.com/mbalazy/claude-skills), not in this repository. [solo-and-batch.md](docs/solo-and-batch.md)
- **Batches and epics through headless workers.** `pm run-epic` drives a tracker's subtasks through isolated `claude -p` workers, one fresh process per sub. Integration mode merges them onto one epic branch and ends in one draft PR; independent mode (`epic_mode: independent`) gives every sub its own branch off the base, pushes whatever carries commits and merges nothing. `pm finish` runs the acceptance as a third run kind, chained automatically with `finish_mode: auto`. Worktree slots, hook-enforced review, a verification baseline. Frozen in favour of solo since 2026-09-09 and hidden in the cockpit unless `cockpit.show_executor` is on; complete from the CLI and the board. [executor.md](docs/executor.md)
- **Two records a project keeps.** A journal of how one repeatedly-troublesome subsystem actually behaves, and a timeline of what happened to the project, with state snapshots whose every line says whether it was verified or assumed. [journals-and-timeline.md](docs/journals-and-timeline.md)
- **A CLI for all of it.** Every surface above is reachable from the shell, and `pm today --json` is the cockpit's queue byte for byte. [cli.md](docs/cli.md)

## Try it in 5 minutes

`PM_DATA_DIR` relocates the data directory, so nothing below touches `~/.claude`.

```sh
export PM_DATA_DIR=$(mktemp -d)
pm init                              # create the data directory
pm projects add demo --path "$PWD"   # one project per repo
pm add demo "Try pm for a day"       # -> demo-1
pm mv demo demo-1 doing
pm list                              # the table
pm context                           # the rollup an agent reads at startup
cat "$PM_DATA_DIR"/demo/demo-1-*.md  # ... which is this file
pm board                             # the TUI; q, then q again, leaves
```

## Install

Go 1.24+ and `git`, plus `gh` for pull requests. The MCP server works with any MCP client; the parts that *launch* agents - the board's launch menu, solo, the executor - drive the [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (the board can also launch [Codex](https://github.com/openai/codex)).

```sh
go install github.com/mbalazy/pm-cli/cmd/pm@latest
```

The binary lands in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when that is empty; put it on your `PATH`. The cockpit's front end is a Node build, so it is in the binary only after `make install-full` from a clone; a `go install` binary serves the API and says so on its front page.

Then register the MCP server with your client and give the agent its usage contract. For Claude Code:

```sh
claude mcp add --transport stdio --scope user pm -- pm mcp
pm docs guide >> ~/.claude/CLAUDE.md
```

For Codex:

```sh
codex mcp add pm -- pm mcp
pm docs guide >> ~/.codex/AGENTS.md
```

The first line writes `[mcp_servers.pm]` with `command = "pm"` and `args = ["mcp"]` into `~/.codex/config.toml`; if `pm` is not on the `PATH` Codex launches with, put the full path in `command`. Codex asks before every tool call unless told otherwise, and pm's read tools are safe to wave through, so a `[mcp_servers.pm.tools.<tool>]` block with `approval_mode = "approve"` per tool (or `default_tools_approval_mode` on the server) is the setting most people end up with. The guide goes into whichever AGENTS.md Codex reads for the project: `~/.codex/AGENTS.md` for every project, or the repository's own.

The second line is not optional for either client: the MCP server gives the agent the *tools*, the guide gives it the *workflow* - when to record what, the brief format, the Spec/Log write rules, and who closes a task. One block, wrapped in `<!-- pm:agent-guide:start/end -->` markers, the same bytes for every client (`pm docs claude` is the older name and still works); to refresh it after an upgrade, delete the block and append again.

Finally, one project per repo:

```sh
pm projects add <slug> --path /path/to/repo
```

`--path` matters: cwd auto-detection matches against it, so a project without one is never found from its own repo. After that you mostly stop operating pm by hand: "add a task: fix the login flow", "what am I working on?", "save a brief, I'm done for today".

The procedures the agent follows when nobody watches are not in this binary: `/solo`, the acceptance `pm finish` runs, the simulator and browser verification live in [mbalazy/claude-skills](https://github.com/mbalazy/claude-skills). Without them a launch starts a session with nothing to follow, so install them once - `install.sh` links every skill into `~/.claude/skills` (or the config dir in `CLAUDE_CONFIG_DIR`) and a `git pull` there updates them in place:

```sh
git clone https://github.com/mbalazy/claude-skills
./claude-skills/install.sh
```

`pm executor doctor <project>` warns while `solo` or `batch-finish-auto` is missing, and so does the cockpit's launch preview.

## Status

A personal tool, in daily use since February 2026; releases are one line each in [CHANGELOG.md](CHANGELOG.md). One machine, one user: no sync, no account, no API key - the agent parts drive the `claude` CLI, so they run on a Claude subscription. Developed on macOS; CI runs the suite on Linux, but the desktop bits are untested there. Not looking for contributions, though bug reports are welcome. Versions promise each other nothing, except that task files stay readable: markdown.

## Docs

| | |
|---|---|
| [docs/cli.md](docs/cli.md) | every command and flag |
| [docs/data-model.md](docs/data-model.md) | the files: task fields and write rules, `project.yaml`, `config.yaml`, the attention queue |
| [docs/mcp.md](docs/mcp.md) | the 14 tools as a reference |
| [docs/board.md](docs/board.md) | the TUI: views, keys, launchers |
| [docs/cockpit.md](docs/cockpit.md) | `pm serve`: screens, routes, then the design record |
| [docs/journals-and-timeline.md](docs/journals-and-timeline.md) | the two per-project records |
| [docs/solo-and-batch.md](docs/solo-and-batch.md) | solo shifts and executor batches: how to start one, where the result shows up |
| [docs/executor.md](docs/executor.md) | the executor: manual, then internals |
| [docs/development.md](docs/development.md) | build, test, CI, release |
| [docs/design-log.md](docs/design-log.md) | why the rules are what they are |
| [docs/README.md](docs/README.md) | the index, with the two files the binary embeds for the agent |

Developed with Claude Code; `CLAUDE.md` is the agent's working memory and doubles as the contributor guide.

## License

[MIT](LICENSE)
