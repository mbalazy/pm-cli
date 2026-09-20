# pm

**pm** is a task tracker that lives in markdown files and speaks MCP. Claude Code reads and updates your tasks as a side effect of the conversation; you keep a kanban TUI, a web cockpit and `grep`. One Go binary, files under `~/.claude/pm/`, no cloud, no database, no account.

[![CI](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mbalazy/pm-cli/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/mbalazy/pm-cli)](go.mod)
[![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

<!-- screenshot: docs/img/board.png -->

## Why files

A task is a markdown file with YAML frontmatter, next to its project's config in `~/.claude/pm/<project>/`. `grep` works, `git` works, any editor is a client.

```markdown
---
id: my-app-12
title: Fix login flow
status: doing
status_changed: '2026-09-02T11:20:04+02:00'
waiting_for: 'review by the lead, PR #940'
brief: 'Worker merged on feat/fix-login. JWT refresh was the culprit ...'
ac: |
  - refresh token rotates on use
  - expired session redirects to /login
links: {pr: 'https://github.com/...'}
branch: feat/fix-login
parent: my-app-10
order: 20
depends_on: [my-app-11]
mode: auto
model: sonnet
---
<!-- spec:start -->
Current-truth spec, rewritten wholesale as decisions land.
<!-- spec:end -->
Append-only log: session notes, worker run records, history.
```

Per-field write rules are what make this survive many sessions:

| Field | Write rule | Purpose |
|---|---|---|
| `brief` | overwrites | "where we left off", the next session's cold start |
| `ac` | overwrites | acceptance criteria; an agent treats them as a hard scope bound |
| Spec zone | rewritten in place | the living document; answers get folded in, not appended |
| Log zone | append-only | the audit trail; never rewritten |
| `links` | merge-only | keys are never removed by an update |

`waiting_for` names who or what blocks a task and is never auto-cleared. Statuses are per-project (default `todo / doing / waiting / done`; an epic's subtasks usually add `merged` and `pushed`) and every write path validates against the set. Files are never migrated: an older one lacks the newer fields, and readers report that as unknown instead of guessing.

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

The binary lands in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when that is empty; put it on your `PATH`. To work on pm, clone and use `make install`, which stamps `VERSION` through ldflags. Then register the MCP server, give the agent its usage contract and create a project:

```sh
claude mcp add --transport stdio --scope user pm -- pm mcp
pm docs claude >> ~/.claude/CLAUDE.md
pm projects add <slug> --path /path/to/repo
```

The second line is not optional: the MCP server gives the agent the *tools*, the guide gives it the *workflow* - when to record what, the brief format, the Spec/Log write rules, and the rule that only a human closes a task. It ships inside the binary between `<!-- pm:agent-guide:start/end -->` markers, versioned as [docs/agent-guide.md](docs/agent-guide.md). `--path` matters: cwd auto-detection matches against it, so a project without one is never found from its own repo.

## Claude Code drives it

After that you mostly stop operating pm by hand:

> "create a pm project for this repo" -> `pm_create_project`
> "add a task: fix the login flow" -> `pm_add_task`
> "what am I working on?" -> `pm_context`
> "save a brief, I'm done for today" -> `pm_update_task`

`pm mcp` is a stdio MCP server with 14 tools: those four, six more for tasks and projects, and the journal and timeline pairs. It runs on an output budget, because a rollup that runs at every session start is paid for every time: `pm_context` caps bodies and collapses finished trackers, `pm_list_tasks` returns the 50 newest in an explicit `{total, shown, note}` wrapper. The same rollup is offline as `pm context [project]`, for when the MCP process holds a stale binary.

## The board

`pm board` (or bare `pm`) opens the TUI: per-project tabs, vim keys, eight views - board, task detail, archive, project info, focus, the live run dashboard, the cross-project run list and the acceptance report.

It is also the control room. A launch overlay starts unattended runs in the background with live worktree-slot occupancy; the detail view renders a run's heartbeat age and per-subtask outcomes; `W` tails a worker's transcript, `K` kills a run and keeps the books straight. It also starts interactive Claude Code sessions on a task - fresh, resumed by session id, or in a worktree - and links the session back.

## The cockpit

<!-- screenshot: docs/img/cockpit-today.png -->

`pm serve --addr 127.0.0.1:7070` serves a JSON API and the React cockpit. The front end is a Node build, so it is in the binary only after `make install-full` from a clone; a `go install` binary serves the API and says so on its front page. Ten screens; the one that matters is Today, an attention queue computed from local files: failed runs, work that landed with no acceptance, tasks waiting on a person (with the age, and an alarm when nobody wrote down who), the focus plan, stuck projects. `/api/events` says *what* changed, so the page refetches itself.

A change feed answers "what happened since yesterday evening" from pm's files, `git` and GitHub (`gh`). Two more are off until you turn them on: Slack, which goes through your own Slack MCP server from `~/.claude.json` so pm keeps no copy of your tokens, and an LLM pass that turns the feed into a short written report and spends tokens doing it.

**Security: there is none.** No auth, no TLS, and the API is not read-only - its POST routes edit tasks, start and kill runs, launch sessions and can spend tokens. The only guard is an `X-PM-Client` header, which stops a stray cross-site form, not a person. Bind it to localhost and reach it over a tunnel you trust.

## Unattended work

pm has two ways to hand tasks to an agent while you are away. The **executor** (`pm work`, `pm run-epic`, `pm finish`) runs each task in a fresh headless `claude -p` worker: opt-in worktree slots (`--additional`) so two runs need not share a checkout, a review policy enforced by a hook rather than by asking the worker nicely, a verification baseline captured once per run so inherited breakage cannot count against the worker, then an acceptance run. **Solo** is the other mode - one long-lived session working a queue under a guard hook that blocks pushes and pull requests unless the launch says otherwise, and destructive git always. The executor journals every run it makes; a solo shift leaves a state file and a written report.

The skills those modes invoke - the acceptance procedure behind `pm finish`, the solo procedure itself - live in the author's Claude configuration, not here: what is here launches them, guards them and records what they did. Internals: [docs/executor.md](docs/executor.md), [design log](docs/design-log.md).

## Also in the box

- **Journals** (`pm journal`) - an append-only record of how one repeatedly-troublesome subsystem actually behaves: symptom, the false conclusion it caused, the real cause, the fix. An entry with no fix is open, and the open set is the backlog.
- **Timeline** (`pm timeline`) - what happened *to* a project, plus dated state snapshots whose every line carries its provenance: `[verified YYYY-MM-DD by <command>]` or `[assumed]`, a verified line older than a week flagged for re-checking.

## CLI reference

**Tasks** - `pm add <project> <title>`, `pm list`, `pm show <project> <task-id>`, `pm mv <project> <task-id> <status>`, `pm done <project> <task-id>`, `pm edit <project> <task-id>`, `pm reorder <parent> <child-id>...`

**Projects and setup** - `pm init` (the data directory, not a project), `pm projects` / `add <slug> --path <repo>` / `edit <slug>`, `pm config show`, `pm docs claude`, `pm docs authoring`, `pm session-id`

**Reading** - `pm board [project]` (or bare `pm`), `pm context [project]`, `pm today`, `pm runs`, `pm journal` (`list`/`show`/`add`/`stats`), `pm timeline [project]` (`add`/`list`)

**Agents** - `pm work [project] <task-id>`, `pm run-epic [project] <tracker-id>`, `pm finish [tracker]`, `pm executor` (`init`/`show`/`doctor`/`stats`), `pm mcp`, `pm serve`

`pm help <command>` and `pm completion <shell>` come from cobra; `--help` prints any command's flags.

## Design notes

A few principles this codebase holds onto, learned from real runs:

- **Write rules per field, not per file.** Append-only rot killed every long-lived task body until the body split into a rewritable Spec and an append-only Log with opposite rules. Brief overwrites; links merge. Every surface (CLI, TUI, MCP, executor) enforces the same rules.
- **Generated beats hand-maintained.** Tracker rollups are computed on read. The one hand-maintained status table this project ever had desynced; it is not coming back.
- **Judge workers on new failures only.** A red test suite the worker did not break must not fail the worker - and without a captured baseline, every worker re-diagnoses the same inherited breakage. The baseline made that structural.
- **Uncertainty is output, not noise.** Best-effort workers must ship their doubts in machine-readable form: `ASSUMPTION:` entries carry a concrete "- verify: how" check, `SPEC-CONFLICT:` flags a spec premise the evidence refuted, `TODO:` is the explicit human handoff. A wrong assumption buried inside working-looking code is the worst bug an agent can leave behind.
- **Observability is best-effort; correctness is not.** Run state and journal writes never abort a run. Locks, atomic renames and status validation always do their job.
- **The journal is the flywheel.** Every prompt rule above traces to a logged failure. Durable run history plus a retro habit is what lets the system get smarter instead of repeating itself.

## Status

A personal tool, in daily use since February 2026, currently 0.64.0. One machine, one user: no sync, no account, no API key - the agent parts drive the `claude` CLI, so they run on a Claude subscription (the solo mode's `claude --bg` needs Claude Code 2.1.272 or newer). Developed on macOS; CI runs the suite on Linux, but the desktop bits are untested there. Not looking for contributions, though bug reports are welcome. Versions promise each other nothing, except that task files stay readable: markdown.

## Development

```sh
make check        # gofmt-check + vet + staticcheck + go test
make test-race    # the race detector, also run by CI
make install      # build and install, version through ldflags
make web-install  # npm ci in web/ (once)
make web-check    # lint, tsc and vitest for the web
make install-full # the front end plus the binary
```

Git hooks live in `githooks/` (`git config core.hooksPath githooks`) and pre-commit runs `make check`, the same target as CI. `make check` and `make install` are node-free on purpose, which is why the cockpit's front end has its own target. The tests need `git` and `python3` on `PATH`, and the agent paths run against a fake `claude` binary - a PATH-prepended script emitting a canned result envelope - so the real subprocess, parsing, journal and run-state plumbing is covered at zero token cost.

Developed with Claude Code; `CLAUDE.md` is the agent's working memory and doubles as the contributor guide. The rest of the writing is indexed in [docs/](docs/README.md).

## License

[MIT](LICENSE)
