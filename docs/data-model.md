# Data model

What pm writes to disk and the rules every writer follows: the data directory, the task file and its per-field write rules, `project.yaml`, the global `config.yaml`, the attention queue those files feed, and the locks around them. For anyone who edits the files by hand, greps them, or writes a client. The rationale behind the rules is in [design-log.md](design-log.md); the agent-facing summary is what `pm docs guide` prints.

## Where the data lives

The root is `$PM_DATA_DIR` when set, else `~/.claude/pm` (`internal/storage/store.go`). `pm init` creates it. A **project** is a directory directly under the root that contains a `project.yaml`; files at the root are never mistaken for projects.

Three invariants hold for everything below:

- **Files are never migrated.** A task written before a field existed lacks it, and every reader treats the missing field as *unknown*, never as a default it can guess.
- **Racy writes take the project lock and re-read inside it** (see Locking). The acceptance claim deliberately does not, so it can outlive its process and be visible from another host.
- **Writes are atomic**: temp file + rename. A crash leaves the old file, never half of the new one.

### Root-level files

| path | writer | content |
|---|---|---|
| `config.yaml` | `pm config`, the cockpit settings screen, or by hand | global config: `remotes`, `cockpit` (see config.yaml below) |
| `focus.yaml` | the board (`t`), the cockpit, `pm today` reads it | `{date, tasks[]}` - today's focus plan |
| `daily.yaml` | legacy; read as a fallback, deleted after the first successful `focus.yaml` write | |
| `tui-config.yaml` | the board's project picker | `{hidden_projects[], project_order[]}` - UI state, never a fact about a project |
| `.cockpit/dismissed.json` | the cockpit's dismiss action | `{rows: {"<section>\|<project>\|<id>\|<since>": "<RFC3339>"}}` |
| `.cockpit/changes/state.json`, `prs.json`, `<YYYY-MM-DD>.jsonl` | the change feed (`pm serve`) | feed cache; the attention queue reads its digest |

### A project directory

| path | purpose |
|---|---|
| `project.yaml` | the project's config (below) |
| `<id>-<title-slug>.md` | one task; frontmatter + body |
| `.pm.lock` | the flock file for the project lock |
| `.journal/<name>.jsonl` | one subsystem journal per declared name, append-only |
| `.timeline/<YYYY-MM>.jsonl` | the project timeline, one file per month of the entry's own timestamp |
| `.executor/<id>.json` | live run-state of a `pm work` / `pm run-epic` run, rewritten whole on every heartbeat |
| `.executor/<id>.log` | that run's stdout and stderr |
| `.executor/<id>.finish.json`, `.finish.log`, `.finish.md` | the acceptance run's state, log and markdown report |
| `.executor/<tracker>.finish.claim` | the acceptance claim - a TTL lock, readable across hosts |
| `.executor/journal.jsonl` | the executor journal: every run's start / end / killed / crashed, append-only |
| `.executor/review-<session>.jsonl` | review telemetry written by the `pm worker-guard` hook |
| `.sessions/slot-<N>.lock` | an interactive session holding a worktree slot from the board |
| `.shift/<YYYY-MM-DD>-<id>.md`, `...-report.md` | a solo shift's state and report; written by the solo skill, read leniently by pm |

A worktree slot also carries `<worktree>/.pm-executor.lock` in the checkout itself, guarding the directory rather than the project (`internal/storage/worktree.go`).

## A task file

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

`ReadTask` refuses a `.md` with no frontmatter block; a directory scan skips an unreadable task with a warning on stderr, never silently (`internal/storage/task.go`).

### Every frontmatter key

| key | type | meaning |
|---|---|---|
| `id` | string | the task id, also the leading component of the file name; `<prefix>-<n>` when minted by pm, `<parent>-<n>` for a subtask |
| `title` | string | |
| `status` | string | one of the project's statuses, or `archived` |
| `created` | date `YYYY-MM-DD` | |
| `updated` | RFC3339, local zone | moves on **every** edit |
| `status_changed` | RFC3339 | the last time `status` actually changed; the "stuck since" clock. Empty on files written before the field existed: that means *unknown*, never "now" |
| `links` | map string -> string | freeform key = url; merge-only |
| `branch` | string | git branch of the work |
| `parent` | string | parent task id; makes this a subtask |
| `tags` | list | replaced wholesale on update |
| `brief` | string | cold-start summary, overwritten |
| `ac` | string | acceptance criteria, overwritten |
| `waiting_for` | string | who or what blocks the task; free text, never validated, never required (not even on `waiting`), never auto-cleared |
| `order` | int | sort order within a column or a parent's rollup; 0 = unset; convention 10, 20, 30 |
| `sessions` | list | Claude Code session ids; append-only |
| `depends_on` | list | sub ids that must land first; `pm run-epic` parks a sub whose dependencies are not merged or done |
| `mode` | `auto` / `manual` | `manual` is a permanent human gate for the executor; empty = `auto` |
| `model` | string | per-task worker model override |
| `epic_mode` | `""` / `independent` | tracker only: integration mode (shared `epic/<tracker>` branch, one PR) or batch mode (own branch per sub, pushed, nothing merged) |
| `finish_mode` | `auto` / `off` / `""` | tracker only: `auto` chains a detached `pm finish` after the run |
| `runtime` | `on` / `off` / `""` | `on` opts the task into the executor's runtime phase. A field rather than a tag because tags replace wholesale |

Every write re-validates `mode`, `epic_mode`, `finish_mode` and `runtime` (`ValidateMode` and friends in `internal/storage/task.go`). A task id must match `^[A-Za-z0-9][A-Za-z0-9._-]*$` - uppercase and digits-only are legal so an imported ticket key like `ACME-253` works; spaces and path separators are not.

### Statuses

- Defaults: `todo`, `doing`, `waiting`, `done` (`storage.DefaultStatuses` in `internal/storage/project.go`). A project's `statuses:` in `project.yaml` overrides them; an empty list means the defaults.
- `archived` is **system-level**: it is never listed in a project's statuses and is always a legal move target. Board columns and `pm_list_tasks` exclude it unless asked.
- `merged` and `pushed` are **not** defaults. A project whose subtasks land through the executor opts into them in `statuses:`; they are the *landing statuses* (`Store.GetLandingStatuses`), and code asks that function rather than comparing to the literals.
- Every write path validates the status against the project's set: `pm mv`, the board, `pm_update_task`, `pm_move_task`, and `pm run-epic` at run start.

### Spec and Log zones

The body has two zones with opposite rules (`internal/storage/spec.go`):

- **Spec** - everything between `<!-- spec:start -->` and `<!-- spec:end -->`. Current truth. `ApplySpec` replaces the whole block; resolved questions get folded in, not appended.
- **Log** - everything outside the markers. Append-only history: session notes, worker run records.

When the markers are absent and a spec is set for the first time, a new block is prepended and the existing body becomes the Log. The board renders the markers as visible headers.

### Stamps

| stamp | shape | rule |
|---|---|---|
| `created` | `YYYY-MM-DD` | set once |
| `updated` | RFC3339 with the local offset | restamped on any edit; the sort key for "most recent first" |
| `status_changed` | RFC3339 | written by `Task.SetStatus` only when the status actually changes; a move to the same status does not reset it. Set at creation from the same clock read as `updated` |

Older files carry a bare date in `updated`; readers parse both shapes (`storage.ParseStamp`) and a bare date sorts before the same day's timestamps. An empty `status_changed` is reported as unknown ("since ?") - a reader never falls back to `updated`, which moves on any edit.

## Parent and subtask rollup

A task is a **tracker** when another task names it as `parent`, that parent exists, and it is not archived. `storage.BuildTrackers` (`internal/storage/tracker.go`) computes the rollup that `pm context` and `pm_context` print; nothing hand-maintains a status table in the parent body.

- Children sort by `order` ascending (0 first), then by the numeric id suffix, then by `updated` descending (`LessByOrder`, shared with the board columns and `pm reorder`).
- A tracker is **finished** when the parent and every child are terminal (`done`, `archived`, `merged`, or a landing status). A finished tracker drops its children from the rollup and carries `children_omitted: true` - that is the bound on `pm_context`'s growth.
- The children of an emitted tracker, and the tracker itself, are suppressed from the flat task list. An orphan (parent archived or missing) stays in the flat list.
- Tracker briefs are one line (`brief_line`: first non-blank line, `**` stripped, 120 runes).

## project.yaml

| key | type | meaning |
|---|---|---|
| `name` | string | display name |
| `prefix` | string | task id prefix; defaults to the slug; validated like a task id |
| `path` | string | the local checkout; cwd auto-detection matches against it (the longest matching path wins, `internal/storage/cwd.go`) |
| `repo` | string | repository URL |
| `stack` | string | |
| `links` | map | freeform |
| `tags` | list | |
| `statuses` | list | per-project status set; empty = defaults |
| `notes` | string | |
| `archived` | bool | the project is asleep: out of every cross-project rollup, group and attention queue. Distinct from the board's `hidden_projects` |
| `group` | string | cockpit group slug; empty = the project is its own group |
| `executor` | block | the executor profile (below) |
| `slack` | `{workspace, channels[]}` | the change feed's Slack mapping |
| `gh_account` | string | the `gh` account whose token the feed uses for this repo |
| `journals` | list of `{name, subject}` | the subsystems this project journals; an undeclared name is an error |
| `claude_config_dir` | string | per-project `CLAUDE_CONFIG_DIR` for launched sessions; `~` expanded |

Writes go through `writeProject`: the fresh marshal is merged into the existing YAML node tree, so **comments and unknown top-level keys survive**, cleared known fields drop, and the file is written atomically. A re-run of `pm executor init` over an annotated block keeps the annotations.

### The `executor:` block

An absent block means the defaults; a present one is overlaid on them (`internal/storage/executor.go`).

| key | default | meaning |
|---|---|---|
| `enabled` | `true` | |
| `additional_worktree` | `false` | legacy: one extra worktree is configured (a capability, not "always use") |
| `worktree_path` | `<repo>-additional` | legacy slot path; relative resolves against the repo |
| `worktrees` | - | the slot pool, `[{path, env}]`; supersedes the legacy pair; slot `env` overlays executor `env` |
| `base_branch` | - | fixed fork point; precedence `--base` > this > the checkout's current branch |
| `env` | - | injected verbatim into every spawned `claude`; appended last so it wins |
| `worker_claude_config_dir` | = `claude_config_dir` | slim config dir for headless workers; `pm finish` keeps the project's |
| `model` | `opus` (the command default) | worker model; precedence `--model` > task `model:` > this |
| `effort` | unset | `low` / `medium` / `high` / `xhigh` / `max` |
| `seed_exclude` | the built-in list | untracked dirs NOT copied into a fresh worktree; **replaces** the defaults when set |
| `prepare` | - | shell command run once per run in the claimed slot; failure aborts |
| `rig` | - | runtime proof command; runs once per run when some task has `runtime: on`; exit 0 = rig up, never aborts |
| `runtime_tools` | - | extra `--allowedTools` patterns for `runtime: on` workers; `$SLOT` expands to the worker's tree |
| `baseline` | - | verification command captured before the run; its failures are pre-existing and do not count against the worker |
| `context_repos` | - | read-only reference checkouts, name -> path |
| `handoff` | - | `{playbook, runtime_skill, rig_skill}` |
| `start_status` | `todo` | a sub is ready to pick up |
| `wip_status` | `doing` | |
| `done_status` | `merged` | where a verified sub lands in integration mode |
| `done_status_independent` | `pushed` | where a green sub lands in batch mode |
| `fix_rounds` | `2` | review -> fix loop cap |
| `phases` | generic | `implement`, `test`, `review`, `verify`, `runtime`, `pr` -> a binding each |
| `notes` | - | |
| `timeout` | `120m` (the command default) | a duration **string**; a bare number is refused |
| `review_model` | `opus` | model pinned onto reviewer subagents; `inherit` turns the pinning off |

A phase binding has four states: `{skill: "/name"}`, `{cmd: "..."}`, absent (the generic prompt), or the scalar `false` (skip). `false` marshals back as `false`, so an unrelated project write cannot flip a skipped phase to generic.

## config.yaml

The global file at `<root>/config.yaml` (`internal/storage/config.go`). A **missing** file is not an error; an **unparsable** one is, loudly, never a silent degrade. pm owns two top-level keys; any other survives a write. `pm config show` prints the file resolved.

### `remotes:`

A hand-authored list of remote runners; nothing in pm writes it. All four keys are required and validation names the offending entry.

| key | meaning |
|---|---|
| `name` | slug, unique |
| `ssh` | the host as `~/.ssh/config` knows it |
| `pm` | absolute path to the pm binary there |
| `root` | that machine's pm data directory |

`pm runs` and the cockpit's remote fetch run `<pm> runs --json --local` over ssh on each one.

### `cockpit:`

Defaults are applied before decoding, so an absent key keeps its default and an explicit zero wins. `SaveConfig` writes the block **resolved**: after the first save every default is an explicit key, merged into the existing node tree so comments stay.

| key | default | rule |
|---|---|---|
| `groups.<slug>.name` | the slug | display name; membership lives in `project.yaml` |
| `groups.<slug>.order` | `0` = unplaced | placed groups first, then by slug |
| `doing_idle_days` | `7` | a doing task with no activity this long is idle |
| `waiting_highlight_days` | `5` | a waiting task older than this is highlighted |
| `stuck_project_days` | `14` | see the attention queue |
| `cutoff_hour` | `18` | `0`-`23`; the "since yesterday evening" boundary |
| `refresh.every` | `30m` | change feed scheduler interval |
| `refresh.window` | `07:00-20:00` | `HH:MM-HH:MM`, no midnight wrap |
| `sections.<name>` | all on except `new_since_cutoff`, `recent` | toggles over the closed section list; an unknown name is a load error |
| `sources.<name>` | `pm`, `git`, `github` on; `slack`, `report` off | feed sources, closed list |
| `sidebar.variant` | `columns` | `columns` / `plain` / `rail` |
| `sidebar.show_repos` | `true` | |
| `sidebar.sort` | `worst` | `worst` / `last_activity` / `manual` |
| `sidebar.width` | `0` | the SPA's default |
| `git.all_branches` | `false` | |
| `report.model` | `haiku` | the LLM report's model |
| `report.language` | `pl` | |
| `slack.servers[]` | `[]` | `{workspace, claude_server or command, args, env, me}`; workspace unique |
| `slack.claude_config` | `~/.claude.json` | where the Slack MCP server definitions are read from |
| `show_executor` | `false` | puts executor runs back on the cockpit. Off by default: the executor is frozen in favour of solo since 2026-09-09 |

The settings screen writes through `service.UpdateSettings`, which adds a floor of `5m` on `refresh.every` and `1` on the day thresholds.

## tui-config.yaml

`{hidden_projects: [], project_order: []}`, read and written by the board's project picker only (`internal/tui/board/config.go`). Plain write, not merged; an unreadable file degrades to the zero config. Hiding a project here is UI state - `archived: true` in `project.yaml` is the fact.

## The attention queue

`storage.BuildAttention` (`internal/storage/attention.go`) computes one cross-project queue of what needs the human, from local files only: the tasks of non-archived projects, `.executor/` run-states and claims, `focus.yaml`, `group`, and the `cockpit:` block. The change feed's digest is handed in by the caller, never fetched. Three surfaces read the same value: `pm today`, `/api/attention`, and the `attention` block of `pm_context`.

Sections, in display order. A section switched off in `cockpit.sections` is not emitted.

| section | rule | sort |
|---|---|---|
| `needs_me` | trackers only, skipping ones the human closed (`done` / `archived`). Ranks worst first: run failed or crashed; acceptance failed or crashed; open visual claims; landed with no acceptance; acceptance partial; live claim | rank, then oldest first |
| `solo_reports` | a solo shift closed within 14 days that left a report | age ascending |
| `landed_no_pr` | a task on a landing status whose parent was accepted, with no `links.pr` | age descending |
| `focus` | today's plan in plan order; a row carried over from an older plan carries `stale_plan` | plan order |
| `in_progress` | live run-states and live acceptances (running, pid alive) | age descending |
| `waiting` | every `waiting` task; no `waiting_for` = `no_reason` (an alarm); older than `waiting_highlight_days` = `highlight`. Age from `status_changed` only; empty = `age_seconds: null` | oldest first, unknown last |
| `changes` | up to 5 feed rows; `total` is the feed's own count; no feed = a note | feed order |
| `stuck_projects` | no task change for `stuck_project_days`, or every `doing` task idle `doing_idle_days` with nothing of the project on focus. Activity = max(`updated`, `status_changed`) | age descending |
| `new_since_cutoff` | tasks created since the cutoff; off by default | age ascending |
| `recent` | configurable but **not computed**; its `note` says so | - |

`needs_me`, `landed_no_pr` and `in_progress` are computed only when `show_executor` is on; otherwise they carry the note "executor runs hidden".

- Severities: `crit`, `warn`, `info`, `ok`. Flags: `no_reason`, `highlight`, `stale_plan`.
- Actions are a **closed set** decided by the row's state: `open`, `report`, `claim`, `rerun_finish`, `resume_run`, `kill`, `focus_toggle`, `set_waiting_for`, `back_to_todo`, `mark_seen`, `sleep_project`, `open_pr`, `release_claim`, `dismiss`, `open_report`. A UI renders what it is given and invents nothing.
- A dismissed row (`.cockpit/dismissed.json`) leaves the queue once, for `needs_me`, `solo_reports` and `landed_no_pr`; it comes back when its condition starts again.
- `wip` = doing tasks with activity since Monday 00:00 local.
- `Scope(project, group)` narrows the rows and recomputes the totals; `?project=` / `?group=` on the API and `--project` / `--group` on `pm today` go through it.

## Locking

`Store.LockProject(slug)` (`internal/storage/lock.go`) is a blocking, advisory, cross-process `flock` on `<projectDir>/.pm.lock`. Every mutating path holds it around read -> mutate -> write and re-reads the task **fresh inside** it: the MCP mutations, `pm add`, `pm reorder`, `Store.MoveTask`, the executor's result writes.

- Never nest it in one process - a second flock deadlocks.
- It refuses to create a missing project directory: a typo'd slug used to conjure an empty project, and locking a deleted project resurrected it.
- Partial `project.yaml` edits go through `Store.MutateProject`, which locks, re-reads and merges.
- If the lock cannot be taken, pm warns on stderr before any unlocked write.

## Focus plan

`<root>/focus.yaml` = `{date, tasks[]}` (`internal/storage/focus.go`). A missing file is fine; an unparsable one is an error. `Cleanup` drops ids that vanished or reached `done` / `archived`; the plan is stale when its date is not today. `pm_context` shows the plan only for today and only its still-open tasks, briefs one-lined. The board toggles a task with `t` and lists the plan under `T`; the cockpit's Today screen and `pm today` show it as the `focus` section.
