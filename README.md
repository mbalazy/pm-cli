# pm

A local, file-based project manager that doubles as a **control plane for AI coding agents** - with a kanban TUI, an MCP server for Claude Code, and an autonomous executor that runs whole epics through isolated headless `claude -p` workers.

Everything lives in plain markdown files with YAML frontmatter under `~/.claude/pm/`. No cloud, no database, no account. `grep` works, `git` works, and any editor is a valid client.

```
┌─────────────┐   ┌──────────────┐   ┌───────────────────┐
│  pm board   │   │  pm mcp      │   │  pm work /        │
│  (TUI)      │   │  (MCP server │   │  pm run-epic      │
│             │   │  for Claude  │   │  (executor:       │
│  vim keys,  │   │  Code)       │   │  headless claude  │
│  launches   │   │              │   │  workers)         │
│  runs       │   │              │   │                   │
└──────┬──────┘   └──────┬───────┘   └─────────┬─────────┘
       │                 │                     │
       └────────┬────────┴──────────┬──────────┘
                ▼                   ▼
        ~/.claude/pm/<project>/   the repo you work on
        (tasks, config, run       (branches, worktree
         state, journal)           slots, PRs)
```

## Highlights

- **Tasks are markdown files** - YAML frontmatter (id, status, brief, acceptance criteria, links, ...) plus a body split into a rewritable **Spec** zone and an append-only **Log** zone.
- **TUI kanban board** (`pm board`) - vim navigation, per-project tabs, task detail with live executor dashboards, one-key launching of Claude Code sessions and executor runs.
- **MCP server** (`pm mcp`) - 10 tools that let Claude Code read and update tasks mid-conversation, with strict output budgets so a session start costs ~15k chars, not 60k.
- **Autonomous executor** - `pm work` runs one task end-to-end (implement → test → review → fix → verify) in a fresh headless worker; `pm run-epic` drives a whole parent+subtasks epic, merging verified subs into an integration branch or pushing independent branches for a batch of unrelated tickets.
- **Learning loop built in** - every run appends to a durable journal; `pm executor stats` rolls it up, and lessons from real failures are baked back into the worker prompts (see [Design notes](#design-notes)).

## Setup

Requirements: Go 1.24+, git. For the executor: the [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`) and optionally `gh` for PRs.

**1. Build and install:**

```sh
go install github.com/mbalazy/pm-cli/cmd/pm@latest
```

That needs no checkout; the binary reports whatever the module proxy resolved - a release tag when one exists, otherwise a pseudo-version of the commit it was built from. To work on pm, build from a clone instead - `make install` stamps the Makefile's `VERSION` through ldflags:

```sh
git clone https://github.com/mbalazy/pm-cli.git && cd pm-cli
make install          # builds with version ldflags, installs to GOBIN
```

`make install` runs `go install`, which puts the binary at `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when `GOBIN` is unset (that's `~/go/bin` on a stock Go setup). Find yours with:

```sh
go env GOBIN GOPATH   # empty GOBIN -> binary is at $GOPATH/bin/pm
```

Make sure that directory is on your `PATH` - the rest of this guide assumes `pm` is runnable by name.

**2. Register the MCP server** with Claude Code (user scope, available in every project; substitute the path from the step above, e.g. `~/go/bin/pm`):

```sh
claude mcp add --transport stdio --scope user pm -- <path-from-step-1>/pm mcp
```

**3. Teach your agent** - install the usage contract into Claude Code's global memory:

```sh
pm docs claude >> ~/.claude/CLAUDE.md
```

This step is not optional. The MCP server gives the agent the *tools*; the guide gives it the *workflow* - when to proactively record things, the brief format, the Spec/Log write rules, task-authoring discipline (`pm docs authoring`), and the rule that only the human closes tasks. Without it the agent drives the tools blind. The guide is embedded in the binary and wrapped in `<!-- pm:agent-guide:start/end -->` markers - to refresh after an upgrade, delete the block and append again. Source of truth, versioned with the code: [docs/agent-guide.md](docs/agent-guide.md) and [docs/task-authoring.md](docs/task-authoring.md).

**4. Create a project** for each repo you want to track (`pm init` only creates the top-level `~/.claude/pm/` data directory - it does not create a project):

```sh
pm projects add <slug> --path /path/to/repo
```

`--path` is effectively required: cwd auto-detection (`pm_context`, `pm executor init`, ...) matches against it, so a project without one won't be found from inside its own repo. You can also just ask Claude Code ("create a pm project for this repo") once the MCP server is registered - it calls `pm_create_project` for you.

Using the executor? One more step, once per project: `pm executor init <project>` (see [docs/executor.md](docs/executor.md)).

## Quick start

**The primary interface is a conversation.** With the setup above done, you don't operate pm by hand - you just talk to Claude Code and it drives the tools:

> "create a pm project for this repo" → `pm_create_project`
> "add a task: fix the login flow" → `pm_add_task`
> "what am I working on?" → `pm_context` (the session-start rollup)
> "save a brief, I'm done for today" → `pm_update_task` with a where-we-left-off summary
> "done, close it" → `pm_move_task`

Claude reads the context at session start, records decisions into the task as you work, and leaves a brief for the next session - the tracker maintains itself as a side effect of the conversation.

The CLI and TUI cover the moments a conversation doesn't:

```sh
pm board       # the kanban TUI: overview, reordering, launching sessions and executor runs
pm context     # the same rollup as pm_context, offline
pm work / pm run-epic   # hand tasks to autonomous workers (see The executor)
```

Direct CLI equivalents exist for everything (`pm init`, `pm add`, `pm mv`, ... - see [CLI reference](#cli-reference)). Data lands in `~/.claude/pm/<project>/` - one `project.yaml` plus one `.md` file per task, side by side.

## Concepts

### Tasks

A task is a markdown file with frontmatter:

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

Field semantics that make this work across many sessions:

| Field | Write rule | Purpose |
|---|---|---|
| `brief` | overwrites | "where we left off" - the cold-start context for the next session |
| `ac` | overwrites | acceptance criteria - the executor treats these as a hard scope bound |
| Spec zone | rewritten in place | the living document; resolved questions get folded in, not appended |
| Log zone | append-only | the audit trail; never rewritten |
| `links` | merge-only | keys are never removed by updates |
| `waiting_for` | overwrites | who or what the task is blocked on - free text, never validated, never auto-cleared |
| `status_changed` | stamped by pm | when the status last actually changed; "waiting since when", which `updated` cannot answer (it moves on any edit). Empty on tasks older than the field - unknown, never guessed from `updated` |

### Projects and statuses

Statuses are **per-project** (`statuses:` in `project.yaml`, default `todo / doing / waiting / done`). `archived` is system-level and never appears in a project's list. Every mutation path validates the status against the project's set - a typo'd status is rejected instead of silently writing a task no board column renders.

### Trackers (parent + subtasks)

A task becomes a **tracker** when other tasks name it as `parent`. The rollup (progress, per-child status, one-line briefs) is **generated** by `pm context` / `pm_context` - never hand-maintained in the parent body, so it cannot desync. Subtask lifecycle in an epic: `todo → doing → merged → done`. Children order by `order` (then id), settable via `pm reorder <parent> <child>...`.

## The board

`pm board` (or bare `pm`) opens the TUI: per-project tabs, vim keys, four views (board / detail / archive / project info).

Beyond task management, the board is the **control room for the executor**: a launch overlay starts `pm work` / `pm run-epic` as detached background runs (with per-launch toggles for yolo mode and worktree slots, including live slot occupancy), the detail view renders a live run dashboard with a heartbeat age, `W` tails the worker's transcript live, and `K` kills a run (process group, SIGTERM → SIGKILL) while keeping the books straight.

It also launches interactive Claude Code sessions on a task (fresh, resume by stored session id, or in a git worktree with per-slot env), auto-linking the session id to the task.

## The MCP server

`pm mcp` is a stdio MCP server exposing:

`pm_context` · `pm_list_tasks` · `pm_get_task` · `pm_add_task` · `pm_update_task` · `pm_move_task` · `pm_delete_task` · `pm_list_projects` · `pm_create_project` · `pm_update_project`

Notable behaviors:

- `pm_context` auto-detects the project from the caller's cwd and returns the rollup: doing tasks (with full briefs), trackers, counts. Bodies cap at 2000 runes with a pointer to `pm_get_task`; finished trackers collapse their children. **The rollup is a summary, not a reading surface** - it runs at every session start, so it is kept cheap by construction.
- `pm_list_tasks` defaults to the 50 newest with an explicit `{total, shown, note}` wrapper; briefs compress to one line in listings.
- Update semantics mirror the field rules above: links merge, `body_append` appends to the Log, `spec` rewrites the Spec zone, brief/ac/waiting_for overwrite. Tri-state params (`*string`/`*int`) distinguish "omit" from "clear". `status_changed` is never a param - pm stamps it whenever a status really changes.

The same rollup is available offline via `pm context [project]` - useful when a long-lived MCP process holds a stale binary. The fuller usage contract the agent needs on top of the tool schemas ships via `pm docs claude` (see [Setup](#setup)).

## The web cockpit (`pm serve`)

`pm serve --addr 127.0.0.1:7070` exposes the same data the MCP server returns, over HTTP, for the React cockpit built into the binary: `GET /api/projects`, `/api/tasks?project=&status=&limit=`, `/api/tasks/{project}/{id}`, `/api/context?project=`, `/api/runs` (`?remote=1` to include the remote runners - one ssh round-trip each, never by default), `/api/focus`, and `/api/events`, a Server-Sent Events feed that says *what changed* (`tasks`/`runs` with the project slug, `ping` while idle) so the page refetches. Every JSON body is the DTO the corresponding MCP tool returns; errors are `{"error": "..."}` with 400/404/500. Unauthenticated on purpose, and NOT read-only: the cockpit's POST routes mutate pm data, start and kill runs and can spend tokens. Bind it to localhost and reach it over Tailscale; anyone who can reach the port can do all of that. Anything outside `/api/` serves the bundle (index.html fallback for client-side routes) or a placeholder page until `make web` has built one.

### Web UI

The bundle is a React SPA in `web/` (Vite, TypeScript, Tailwind v4, TanStack Query + Router, shadcn/ui components copied into `web/src/components/ui`, lucide-react icons; npm). Five screens over the same API the MCP server answers from:

- **Today** (`/`) - the attention queue: what needs you (failed runs, open visual claims, work landed without acceptance), accepted work without a PR, today's focus, live runs, tasks waiting on someone (with their age and a "no reason" alarm), the changes since the cutoff, stuck projects. `?g=<group>` narrows every section to one client.
- **Group** (`/g/<slug>`) - one client or product over one or more repos, with Overview (where we left off, needs me + waiting, in progress, trackers), Board, Runs and Changes tabs.
- **Changes** (`/changes`) - the feed of what changed since yesterday's cutoff, from pm itself, git, GitHub and Slack, with an optional LLM report on top.
- **Runs** (`/runs`) - the `pm runs` table ordered "needs me first", with duration, time since the end and heartbeat age; remote runners only on an explicit button.
- **Settings** (`/settings`) - the `cockpit:` block of `config.yaml` and the per-project group, Slack and asleep settings, saved from the browser.

Most mutations - focus, status and blocker reason, brief, notes, marking changes seen, claiming, re-running or killing a run, launching a solo session - go through one confirmation dialog, and a run action shows the exact command before it starts. Dismissing an attention row, saving settings, approving a PR and refreshing the change feed post directly. The look is an editorial ledger: a masthead with the date, sections separated by rules rather than cards, rows as dispatch lines (glyph, project, id and title, the reason, the age, the actions), Fraunces for the headings and Instrument Sans for the rest, warm neutrals in OKLCH, a dark theme that follows the system (or the toggle in the sidebar's footer), state glyphs always next to their colour, and a phone layout from 390 px up. Keyboard: `1`/`2`/`3`/`,` switch screens, `j`/`k`/`Enter` walk the rows, `t` toggles focus, `g <letter>` filters by group, `[`/`]` step a group page's tabs, `?` lists the shortcuts, ⌘K / Ctrl+K opens the palette. The page listens to `/api/events` and refetches what changed, so a board edit or a run's heartbeat shows up within a couple of seconds; a manifest makes it installable on a phone.

```sh
make web-install && make web && make install   # or: make web-install && make install-full
pm serve                                        # http://127.0.0.1:7070
```

Development: run `pm serve` in one terminal and `cd web && npm run dev` in another - Vite proxies `/api` to the server (`PM_API_URL` points it elsewhere). `make web-check` runs lint, tsc and vitest; `make check` and `make install` stay node-free (a binary built without the bundle serves a placeholder page).

## CLI reference

| Command | What it does |
|---|---|
| `pm` / `pm board` | open the TUI board (project auto-detected from cwd) |
| `pm init` | initialize the `~/.claude/pm/` data directory (does not create a project) |
| `pm projects add <slug> --path <repo>` | create a project |
| `pm projects` | list projects |
| `pm add <project> <title>` | add a task (`--order`, `--id`) |
| `pm list [project]` | list tasks |
| `pm show <task>` | print one task |
| `pm mv <project> <task> <status>` | move a task (validated against project statuses) |
| `pm done <task>` | shortcut for moving to done |
| `pm edit <task>` | open the task file in `$EDITOR` |
| `pm reorder <parent> <child>...` | renumber children 10, 20, 30... in the given sequence |
| `pm context [project]` | print the session-start rollup (same data as MCP `pm_context`) |
| `pm work [project] <task>` | run one task through a headless worker |
| `pm run-epic [project] <tracker>` | drive a whole epic (`--then-finish` chains the acceptance) |
| `pm finish [tracker]` | run the acceptance; `claim`/`release`/`status` manage the lock |
| `pm runs` | table of every run + acceptance, all projects, local + remote |
| `pm config show` | print the global config (remote-runner registry) |
| `pm executor init/show/doctor/stats` | executor profile management |
| `pm mcp` | run the stdio MCP server |
| `pm serve` | serve the web cockpit: JSON API (reads, mutations, run control) + SSE change feed + embedded front end (localhost, unauthenticated) |
| `pm docs claude` / `pm docs authoring` | print the embedded agent guide / task-authoring rules |
| `pm session-id` | print the current Claude Code session UUID |

Common executor flags: `--dry-run`, `--model`, `--max-turns`, `--timeout`, `--yolo`, `--additional` (+ `--slot N`, `--base`), `--independent`, `--allow-dirty`, `--no-pr`.

## Data layout

```
~/.claude/pm/
└── <project-slug>/
    ├── project.yaml          # name, path, stack, statuses, links, executor block
    ├── <id>-<slug>.md        # one file per task, next to the config
    ├── .pm.lock              # cross-process flock serializing read-modify-write
    ├── .sessions/            # interactive worktree-slot session locks
    └── .executor/
        ├── <task>.json       # live run state (overwritten)
        ├── <task>.log        # run log for background runs
        └── journal.jsonl     # append-only run history
```

Set `PM_DATA_DIR` to point the whole data directory elsewhere (default `~/.claude/pm/`). Useful for sandboxed verification, and for headless executor workers - they run under a guard that refuses writes anywhere under `~/.claude`, so a worker task that needs to write into the data dir will fail without a relocated `PM_DATA_DIR`.

Concurrency model in one paragraph: task writes are atomic (tmp+rename), so the only hazard is two pm processes interleaving read-modify-write and dropping each other's edits. `Store.LockProject` (an flock on `.pm.lock`) guards every mutating path - MCP handlers, the executor's long-window writers (which re-read the task fresh, since their copy predates a 30+ minute worker run), and `MoveTask` itself. Locks never nest in-process; release closures are idempotent for handoff to self-locking callees.

## Development

```sh
make install               # build + install (VERSION from Makefile, via ldflags)
make check                 # go vet + staticcheck + go test
go test ./internal/... -v  # tests directly
make web-install           # npm ci in web/ (once)
make web-check             # lint + tsc + vitest for the web UI
make web                   # build the bundle into internal/server/dist
make install-full          # web + install
```

- Git hooks are versioned in `githooks/` (`git config core.hooksPath githooks`); pre-commit runs gofmt-check + vet + staticcheck + tests.
- **Executor paths are tested through a fake `claude` binary** - a PATH-prepended script emitting a canned result envelope - so the real subprocess/parse/journal/run-state plumbing runs at zero token cost.
- **MCP handlers have true end-to-end coverage in `internal/mcpserver/e2e_test.go`**, driven over the SDK's in-memory transport so the actual registered closures execute. The older `tools_test.go` invariant tests (links merge, Spec rewrite, Log append, brief lifecycle) are storage-level: they exercise `storage.WriteTask`/`FindTask` directly rather than going through a handler.
- Concurrency invariants (lock races, atomic slot claims) have dedicated hammer tests; run suspects under `-race`.
- Bump `VERSION` in the Makefile on each release.

## Design notes

A few principles this codebase holds onto, learned from real runs:

- **Write rules per field, not per file.** Append-only rot killed every long-lived task body until the body split into a rewritable Spec and an append-only Log with opposite rules. Brief overwrites; links merge. Every surface (CLI, TUI, MCP, executor) enforces the same rules.
- **Generated beats hand-maintained.** Tracker rollups are computed on read. The one hand-maintained status table this project ever had desynced; it is not coming back.
- **Judge workers on new failures only.** A red test suite the worker did not break must not fail the worker - and without a captured baseline, every worker re-diagnoses the same inherited breakage. The baseline made that structural.
- **Uncertainty is output, not noise.** Best-effort workers must ship their doubts in machine-readable form: `ASSUMPTION:` entries carry a concrete "- verify: how" check, `SPEC-CONFLICT:` flags a spec premise the evidence refuted, `TODO:` is the explicit human handoff. A wrong assumption buried inside working-looking code is the worst bug an agent can leave behind.
- **Observability is best-effort; correctness is not.** Run state and journal writes never abort a run. Locks, atomic renames and status validation always do their job.
- **The journal is the flywheel.** Every prompt rule above traces to a logged failure. Durable run history plus a retro habit is what lets the system get smarter instead of repeating itself.

## License

[MIT](LICENSE)
