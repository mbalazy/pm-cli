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

Using the executor? One more step, once per project: `pm executor init <project>` (see [The executor](#the-executor)).

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
waiting_for: 'review Alex PR #940'
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

`pm serve --addr 127.0.0.1:7070` exposes the same data the MCP server returns, over HTTP, for the React cockpit built into the binary: `GET /api/projects`, `/api/tasks?project=&status=&limit=`, `/api/tasks/{project}/{id}`, `/api/context?project=`, `/api/runs` (`?remote=1` to include the remote runners - one ssh round-trip each, never by default), `/api/focus`, and `/api/events`, a Server-Sent Events feed that says *what changed* (`tasks`/`runs` with the project slug, `ping` while idle) so the page refetches. Every JSON body is the DTO the corresponding MCP tool returns; errors are `{"error": "..."}` with 400/404/500. Read-only and unauthenticated on purpose - bind to localhost and reach it over Tailscale. Anything outside `/api/` serves the bundle (index.html fallback for client-side routes) or a placeholder page until `make web` has built one.

## The executor

The executor runs pm tasks autonomously through **isolated headless `claude -p` workers**, so a multi-subtask epic executes without blowing one session's context.

First run:

```sh
pm executor init my-app       # once per project: detect skills/stack, draft the profile
pm work my-app-12 --dry-run   # inspect the full worker prompt + argv, zero tokens
pm work my-app-12             # run one task end-to-end → draft PR
pm run-epic my-app-10         # drive a whole tracker's subtasks
```

Two commands do the work:

### `pm work [project] <task>` - the atom

Spawns one fresh worker in the project directory (so the project's own CLAUDE.md, skills and MCP servers load by cwd). The worker runs the inner loop - **implement → test → adversarial review → fix → verify** - and returns a schema-validated JSON contract: `{status, summary, branch, commits, unresolved}`. Standalone runs end with a draft PR; `--dry-run` prints the full prompt and argv without spending a token.

### `pm run-epic [project] <tracker>` - the manager

Drives a tracker's subtasks sequentially (by `order`) in one of two modes:

**Integration mode** (default) - for one coherent feature. Creates `epic/<tracker>`, branches each sub off it, merges back `--no-ff` on verify-green, parks blocked/failed/conflicted subs on `waiting` and keeps going. Ends with ONE draft epic→main PR. Re-entrant: a re-run skips finished subs. Never merges to main, never closes the parent.

**Independent (batch) mode** (`epic_mode: independent` on the tracker) - for a batch of unrelated tickets. Each sub gets its own branch off the base, the worker runs **best-effort** (never abandons; records `ASSUMPTION:` / `TODO:` / `SPEC-CONFLICT:` entries instead), and every branch carrying commits is pushed - even on failure, since partial work on origin beats work lost to the next branch wipe. Nothing merges; a human finishes each task on its own branch.

### Per-sub gates

- `depends_on: [ids]` - unmet deps skip the sub (no worker, no wasted tokens); a re-run picks it up once deps merge.
- `mode: manual` - a permanent human gate: the manager never touches the sub; you do it by hand and move it to done yourself.
- `model: sonnet` - per-sub model override, so trivial subs run cheap while investigation subs stay on the strong model.

### `pm finish [tracker]` - the acceptance as a run

The third run kind: a headless worker that invokes the global `batch-finish-auto` skill to accept a finished batch - walk the sub branches, verify, push fixes, write a report. Yolo by default (the guard hook still rides along), `--sim` only for attended runs - anything detached keeps its hands off shared runtime. Every acceptance starts by CLAIMING the run (`pm finish claim|release|status <tracker>`): a TTL lock stored beside the run's own state, so two acceptance sessions can never take the same run - even across machines. Its state, log and report live in `<tracker>.finish.json` / `.finish.log` / `.finish.md`, never overwriting the run's files.

Set `finish_mode: auto` on a tracker (or pass `--then-finish`) and `pm run-epic` chains the acceptance itself when the run completes - detached, best-effort, same machine only. `pm runs` (and the board's `R` view) shows every tracker's run and acceptance across all projects, including remote runners registered in the global `~/.claude/pm/config.yaml` (`pm config show`), with the count of unresolved visual claims per acceptance - the morning TODO list.

### Verification baseline - the "new failures only" verdict

If `executor.baseline` is set (typically the project's full verification command), it is captured **once per run** on the branch the work forks from and injected into every worker prompt. Green baseline: any failure the worker sees is new. Red baseline: the listed failures are pre-existing, out of scope, and **must not demote the verdict** - the worker records them once as `PRE-EXISTING:` and moves on. This kills the two classic failure modes of agents in imperfect repos: blaming inherited breakage on themselves, and burning turns re-diagnosing it in every sub.

### Worktree slots (`--additional`)

Opt-in per run. The project defines a pool of worktree slots in `project.yaml` (`executor.worktrees`, each with a path and env overlay - e.g. its own Metro port and simulator UDID). A run claims the first free slot with an atomic, PID-checked lock (claim = `link(2)` of a fully written file; stale locks of dead processes are taken over), reuses the worktree across runs (installed deps survive), gives each task a fresh branch off a wiped tree, and leaves your main checkout completely alone. `executor.prepare` (e.g. `yarn install --frozen-lockfile`) runs once per run in the claimed slot. Interactive board launches share the same slot pool through a separate session-lock registry, so a manual worktree session and a headless run never fight over runtime resources.

### Safety envelope

Workers run under `acceptEdits` with a curated bash allowlist and explicit disallows (force-push, merge to main, hard reset); `--yolo` bypasses. Hard rules in the system prompt: stay strictly within the AC (scope creep is a failure, not a bonus), commit early and often, never switch branches. The executor itself never merges to main and never closes the parent tracker - those calls stay human.

Worker environment: `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` are stripped so headless workers bill the Claude subscription, not the metered API; `PM_HEADLESS=1` lets project hooks skip interactive-only boot work; `CLAUDE_CONFIG_DIR` is pinned when the project uses a non-default Claude account.

### Observability - pm is the bus

- **Run state** (`<project>/.executor/<task>.json`): live JSON the TUI polls - PID, current sub/phase/session, per-sub outcomes. A 30s **heartbeat** re-stamps it while a worker is in flight, so a 40-minute sub is distinguishable from a hung manager. (The stamp proves "the manager is alive inside a worker", not "the worker is progressing" - documented honestly at the source.)
- **Journal** (`<project>/.executor/journal.jsonl`): append-only history of every run - outcomes, durations, turns, cost, flags, run ids (pids get recycled; run ids do not). `pm executor stats` rolls it up: runs by kind, the six-outcome sub histogram (worker-backed vs gate decisions counted separately), duration/turns/cost totals.
- All observability writes are **best-effort by contract**: a failed write costs a stale dashboard, never a wrong run.

### Executor configuration and the handoff contract

The `executor` block in `project.yaml` binds each phase to a skill, a command, the built-in generic, or skip; plus baseline, prepare, base branch, worktree slots, env, read-only `context_repos` (cross-repo facts a worker may read but never touch), and the **handoff**: a pointer to the project's evidence playbook and the skill that drives its runtime (simulator/browser).

```sh
pm executor init <project>    # auto-detects skills + stack, drafts the block,
                              # scaffolds the evidence playbook (TODO slots for prose)
pm executor show [project]    # the profile as RESOLVED: bindings, slots + env, handoff
pm executor doctor [project]  # completeness check: ERRORs are binary filesystem facts,
                              # WARNs are heuristics over prose; --strict promotes warns
pm executor stats [project]   # journal rollup
```

`pm executor init` is re-runnable and merge-aware: detected bindings refresh, hand-set fields survive (comments and unknown keys in `project.yaml` are preserved through a YAML node-tree merge), and hand-written playbook prose is never regenerated.

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
| `pm serve` | serve the web cockpit: read-only JSON API + SSE change feed + embedded front end (localhost) |
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
