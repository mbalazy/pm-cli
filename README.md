# pm

**pm** is a task tracker that lives in markdown files and speaks MCP. Claude Code reads and updates your tasks as a side effect of the conversation; you keep a kanban TUI, a web cockpit and `grep`. One Go binary, files under `~/.claude/pm/`, no cloud, no database, no account.

[![CI](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/mbalazy/pm-cli)](go.mod)
[![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

## What you get

- **Files you can read.** A task is one markdown file with YAML frontmatter next to its project's `project.yaml`. Each field has a write rule that survives many sessions: the brief overwrites, links merge, the Spec zone is rewritten in place, the Log zone only ever grows. `grep` works, `git` works, any editor is a client. [data-model.md](docs/data-model.md)
- **An MCP server.** `pm mcp` gives Claude Code 14 tools: context at session start, tasks, projects, journals, timeline. It runs on an output budget, because a rollup paid at every session start is paid every time. [mcp.md](docs/mcp.md)
- **A board.** `pm` opens a kanban TUI with per-project tabs and vim keys: eight views, from the columns to a live worker transcript, and a launch menu that starts a Claude Code, Codex or executor session on a task and links it back. [board.md](docs/board.md)
- **A cockpit.** `pm serve` is a JSON API plus a React app. Its home screen is Today: one attention queue computed from local files - failed runs, work that landed with no acceptance, tasks waiting on a person, stuck projects. A change feed answers "what happened since yesterday evening" from pm's files, `git` and GitHub. No auth, no TLS; bind it to localhost. [cockpit.md](docs/cockpit.md)
- **Unattended work.** Solo is the current path: one long-lived Claude Code session working a queue under a guard hook, launched from the cockpit, leaving a state file and a written report. The executor (`pm work`, `pm run-epic`, `pm finish`) is the older path - headless workers with worktree slots, hook-enforced review and a verification baseline - still shipped, hidden in the cockpit by default. [executor.md](docs/executor.md)
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

Go 1.24+ and `git`; the agent parts also want the [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI, plus `gh` for pull requests.

```sh
go install github.com/mbalazy/pm-cli/cmd/pm@latest
```

The binary lands in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when that is empty; put it on your `PATH`. The cockpit's front end is a Node build, so it is in the binary only after `make install-full` from a clone; a `go install` binary serves the API and says so on its front page.

Then register the MCP server, give the agent its usage contract and create a project:

```sh
claude mcp add --transport stdio --scope user pm -- pm mcp
pm docs claude >> ~/.claude/CLAUDE.md
pm projects add <slug> --path /path/to/repo
```

The second line is not optional: the MCP server gives the agent the *tools*, the guide gives it the *workflow* - when to record what, the brief format, the Spec/Log write rules, and who closes a task. `--path` matters: cwd auto-detection matches against it, so a project without one is never found from its own repo. After that you mostly stop operating pm by hand: "add a task: fix the login flow", "what am I working on?", "save a brief, I'm done for today".

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
| [docs/executor.md](docs/executor.md) | the executor: manual, then internals |
| [docs/development.md](docs/development.md) | build, test, CI, release |
| [docs/design-log.md](docs/design-log.md) | why the rules are what they are |
| [docs/README.md](docs/README.md) | the index, with the two files the binary embeds for the agent |

Developed with Claude Code; `CLAUDE.md` is the agent's working memory and doubles as the contributor guide.

## License

[MIT](LICENSE)
