# MCP server

The reference for `pm mcp`: the 14 tools the agent gets, every parameter, every output shape, the budgets, and the traps. For anyone writing or debugging an agent against pm. The *workflow* - when to record what, the brief format, who closes a task - is a separate document, [agent-guide.md](agent-guide.md), printed by `pm docs guide` for pasting into the client's instructions file (`CLAUDE.md`, `AGENTS.md`); this file is the contract's reference, that one is the contract.

`pm mcp` is a stdio server (`internal/mcpserver`), so any MCP client that can spawn a process can register it. Once, user-scope, for Claude Code:

```sh
claude mcp add --transport stdio --scope user pm -- pm mcp
pm docs guide >> ~/.claude/CLAUDE.md
```

For Codex (`~/.codex/config.toml` gets `[mcp_servers.pm]` with `command = "pm"`, `args = ["mcp"]`; a per-tool `approval_mode = "approve"` under `[mcp_servers.pm.tools.<tool>]` stops the prompt on every read):

```sh
codex mcp add pm -- pm mcp
pm docs guide >> ~/.codex/AGENTS.md
```

Nothing below depends on the client: the tools, their parameters and their budgets are the same whoever spawned the server. The one Claude-Code-only piece is on the CLI side, `pm session-id` (the source of the `sessions` field), and the guide says so where it names it.

Every tool's semantics live in `internal/service`; the handlers only decode, call and respond. The HTTP API of `pm serve` calls the same functions, so a rule below holds on the web too.

## Rules that hold everywhere

- **Links merge.** An update adds and overwrites keys; a key is never removed.
- **Tags replace** when the parameter is present.
- **`body_append` appends** to the Log zone with a blank line; **`spec` rewrites** the Spec block in place, creating it at the top when absent. The rest of the body is never touched.
- **Tri-state parameters** (`branch`, `parent`, `order`, `mode`, `model`, `epic_mode`, `finish_mode`, `runtime`, `brief`, `ac`, `waiting_for` on update; `group` on project update): omit = keep, empty string (or `0` for `order`) = clear.
- **`title` is never clearable**: an empty value leaves it untouched.
- **Status is validated** against the project's own set (the union of every project's, unscoped); `archived` is always legal. An unknown status is an error, never an empty result.
- **`status_changed` is never a parameter.** pm stamps it when the status actually changes.
- **Required iff the json tag lacks `omitempty`.** The tool schemas are generated from the input structs in `internal/service`, so the "required" column below is that rule applied.
- **Every result is JSON as text**: one `TextContent` holding the marshalled struct. A failure is an MCP tool error whose text is the Go error.
- **Mutations lock the project** and re-read the task fresh inside the lock, so two sessions editing one task cannot lose each other's write.
- **Fuzzy ids**: `pm_get_task`, `pm_update_task` and `pm_move_task` resolve `task_id` as exact id, then unique id prefix, then title substring; an ambiguous query is an error. `pm_delete_task` takes the exact full id only.

## Budgets

A rollup that runs at every session start is paid for every time, so the readers cap themselves (`internal/service/dto.go`).

| constant | value | applies to |
|---|---|---|
| `DefaultListLimit` | 50 | `pm_list_tasks` default page |
| `MaxListLimit` | 200 | `pm_list_tasks` hard cap on `limit` |
| `ContextBodyLimit` | 2000 runes | each doing task's `body` in `pm_context`; the brief is never cut |
| `DefaultJournalLimit` / `MaxJournalLimit` | 20 / 200 | `pm_journal_list` |
| `DefaultTimelineLimit` / `MaxTimelineLimit` | 20 / 200 | `pm_timeline_list` filtered reads |
| `ContextTimelineLimit` | 20 | entries after the state in `pm_context`; the state itself is whole |
| `ContextWaitingLimit` | 10 | waiting rows in `pm_context`'s attention digest |
| brief line | 120 runes | first non-blank line of a brief, in list and tracker rows |

`pm_get_task` is unbounded.

## Tasks and projects

### pm_list_tasks

List tasks, newest first, optionally filtered. Excludes archived unless `status` is `archived`.

| param | type | required | notes |
|---|---|---|---|
| `project` | string | no | slug or prefix; omit for every project |
| `status` | string | no | validated against the project's set, or the union when unscoped |
| `limit` | int | no | default 50, cap 200; the result says total vs shown |

Output `{tasks[], total, shown, note?}`. A task row is a **summary**: `id, title, status, project, updated, branch?, parent?, order?, tags?, links?, brief?, ac?, waiting_for?, status_changed?, session_count`. The brief is one line; `ac` is full. `status_changed` absent means unknown - never read `updated` in its place.

### pm_get_task

Full task including the body. Fuzzy `task_id`.

| param | type | required |
|---|---|---|
| `project` | string | yes |
| `task_id` | string | yes |

Output: a **detail** = the summary fields plus `created, body?, sessions?, depends_on?, mode?, model?, epic_mode?, finish_mode?, runtime?`.

### pm_context

The session-start rollup. Explicit `project` wins; else `cwd` is matched against every project's `path` (longest match); a miss falls back to the cross-project shape with a `note`.

| param | type | required |
|---|---|---|
| `project` | string | no |
| `cwd` | string | no |

**The output has two shapes**, and a client must branch on which it got.

Project-scoped: `project {slug, name, repo?, stack?, notes?, links?, statuses}`, `task_counts`, `doing_tasks[]` (details, body cut at 2000 runes, brief FULL; children and trackers suppressed), `trackers[]?` (the parent + subtask rollup), `focus_tasks[]?` (today's plan only), `attention?` + `attention_note?`, `journals[]?` (counts only: `name, subject?, total, open`) + `journals_note?`, `timeline?` (the state whole, up to 20 entries after it, `since_omitted`, `verification`, `stale`, `note`), `executor_profile?` (a pointer string telling you to run `pm executor show`, not the profile).

Cross-project: `projects[]` of `{slug, name, repo?, task_counts, doing_tasks[]? (summaries, briefs one-lined), trackers[]?, timeline_state?}`, `focus_tasks[]?`, `attention?`, `note?`.

The `attention` digest is `{wip, needs_me[], waiting[] (at most 10, no_reason rows first), waiting_total, stuck_projects[], counts{}, note?}` - the same queue the cockpit's home screen and `pm today` show, narrowed to the project when scoped. A row is `{section, severity, project, group, task_id?, title, status?, reason, waiting_for?, age_seconds (null = unknown), since?, flags[]?, actions[]}`.

A tracker in the rollup is `{id, title, status, brief_line?, total, progress{}, children[]?, children_omitted?}`; a finished tracker drops its children.

### pm_list_projects

No parameters. Output `{projects[], note?}` with a row per project: `slug, name, stack?, archived?, group, group_name, task_counts`. A project whose `project.yaml` or task directory fails to read is skipped and named in `note` instead of vanishing.

### pm_add_task

Create a task. The body must hold only verified facts from the conversation (the tool description says so, `pm docs authoring` says why).

| param | type | required | notes |
|---|---|---|---|
| `project` | string | yes | |
| `title` | string | yes | |
| `status` | string | no | default: the project's first status |
| `branch` | string | no | |
| `parent` | string | no | makes it a subtask; the id is then minted as `<parent>-<n>` |
| `order` | int | no | 10, 20, 30 convention; 0 = unset |
| `depends_on` | []string | no | sub ids that gate `pm run-epic` |
| `mode` | string | no | `auto` (default) / `manual` |
| `model` | string | no | worker model override |
| `epic_mode` | string | no | tracker only: `""` / `independent` |
| `finish_mode` | string | no | tracker only: `auto` / `off` / `""` |
| `runtime` | string | no | `on` / `off` / `""` |
| `tags` | []string | no | |
| `links` | map | no | |
| `body` | string | no | the Log zone |
| `spec` | string | no | the Spec block |
| `id` | string | no | explicit id; validated as a file-name component; omit to mint `<prefix>-<n>` |
| `brief`, `ac`, `waiting_for` | string | no | |
| `sessions` | []string | no | |

`status`, `mode`, `epic_mode`, `finish_mode` and `runtime` are checked **before** the file is claimed, so a bad value never leaves a zero-byte phantom file. Output: the task detail.

### pm_update_task

| param | type | required | rule |
|---|---|---|---|
| `project` | string | yes | |
| `task_id` | string | yes | fuzzy |
| `status` | string | no | validated; stamps `status_changed` on a real change |
| `title` | string | no | empty = untouched |
| `branch`, `parent`, `brief`, `ac`, `waiting_for` | string | no | tri-state |
| `order` | int | no | tri-state, 0 clears |
| `mode`, `model`, `epic_mode`, `finish_mode`, `runtime` | string | no | tri-state, validated |
| `depends_on` | []string | no | replaces; `[]` clears |
| `tags` | []string | no | replaces when present |
| `links` | map | no | merges |
| `body_append` | string | no | appends to the Log |
| `spec` | string | no | rewrites the Spec |
| `sessions` | []string | no | appends |

`updated` is restamped on every call. Output: the task detail.

### pm_move_task

| param | type | required |
|---|---|---|
| `project` | string | yes |
| `task_id` | string | yes |
| `new_status` | string | yes |

Output `{id, new_status, old_status}`. `waiting_for` is not on this tool: to park a task with its blocker in one call use `pm_update_task` with `status` and `waiting_for` together.

### pm_delete_task

Permanent, no undo, **exact full id only** - a prefix or a title errors with nothing deleted.

| param | type | required |
|---|---|---|
| `project` | string | yes |
| `task_id` | string | yes |

Output is a string map kept byte-for-byte for old clients: `{"id": "...", "title": "...", "deleted": "true"}` - `deleted` is the **string** `"true"`, not a boolean.

### pm_create_project

| param | type | required | notes |
|---|---|---|---|
| `slug` | string | yes | a safe directory name: lowercase alnum, `.`, `_`, `-` |
| `name` | string | no | defaults to the slug |
| `path`, `repo`, `stack`, `notes` | string | no | `path` is what cwd detection matches |
| `prefix` | string | no | defaults to the slug; the id source |
| `links` | map | no | |
| `tags` | []string | no | |
| `statuses` | []string | no | default `todo, doing, waiting, done` |
| `group` | string | no | cockpit group slug |

Rejects an unsafe slug, group or prefix, and a slug that differs from an existing one only by case. Output: `{slug, name, path?, repo?, stack?, notes?, prefix?, links?, tags?, statuses?, archived?, group?, slack?}`.

### pm_update_project

| param | type | required | rule |
|---|---|---|---|
| `project` | string | yes | |
| `name`, `path`, `repo`, `stack`, `notes`, `prefix` | string | no | overwrite when non-empty |
| `links` | map | no | merge |
| `tags` | []string | no | replace |
| `statuses` | []string | no | replace; `[]` resets to the defaults |
| `archived` | bool | no | the asleep flag |
| `group` | string | no | tri-state; `""` leaves the group |
| `slack` | `{workspace?, channels[]?}` | no | an empty mapping removes the key |

A `statuses` replacement that would leave a task on a status no longer in the list is refused - move or close those tasks first. Output: the project result above.

## Journals

A journal is a per-project append-only record of one repeatedly-troublesome subsystem, declared in `project.yaml` under `journals:`; an undeclared name is an error. Details in [journals-and-timeline.md](journals-and-timeline.md).

### pm_journal_add

| param | type | required | notes |
|---|---|---|---|
| `project` | string | yes | |
| `name` | string | yes | declared in `project.yaml` |
| `symptom` | string | yes | |
| `false_conclusion` | string | no | the wrong belief the symptom produced |
| `cause` | string | no | |
| `cost_min` | int | no | minutes lost |
| `fix` | string | no | the rule it produced; **empty = OPEN** |
| `tags` | []string | no | |
| `session` | string | no | |
| `date` | string | no | `YYYY-MM-DD` back-date when seeding history |
| `resolves` | []string | no | ids of earlier entries this one closes; an unknown id is refused |

Output `{project, journal, entry, total, open, note}`; the note reports the recurrence the entry produced ("entry 7; 3 open; 4 entries share tag ...").

### pm_journal_list

| param | type | required | notes |
|---|---|---|---|
| `project` | string | yes | |
| `name` | string | no | omit to list the declared journals with counts |
| `open` | bool | no | only entries with no fix (the backlog) |
| `limit` | int | no | default 20, cap 200 |

Output `{project, name, subject?, total, shown, open, closed_later?, cost_min?, tags[]?, months[]?, entries[], note?, declared[]?}` - the stats travel with the entries. An entry is `{id, ts, symptom, false_conclusion?, cause?, cost_min?, fix?, tags?, session?, resolves?}`, newest first.

## Timeline

What happened TO a project (an event, a decision) plus dated `state` snapshots; what happened IN a task goes to the task's Log. Details in [journals-and-timeline.md](journals-and-timeline.md).

### pm_timeline_add

| param | type | required | notes |
|---|---|---|---|
| `project` | string | yes | |
| `kind` | string | yes | `event` / `decision` / `state` |
| `text` | string | yes | a state is 10-20 lines, every line ending `[verified YYYY-MM-DD by <source>]` or `[assumed]` |
| `refs` | []string | no | task ids, paths, URLs, stored verbatim |
| `session` | string | no | |

A `[verified]` marker with no day or a future day is refused. Output `{project, entry, stale, entries_since, days_since, note}`; for a state the note carries the verification summary.

### pm_timeline_list

| param | type | required | notes |
|---|---|---|---|
| `project` | string | yes | |
| `since` | string | no | `YYYY-MM-DD` |
| `kind` | string | no | |
| `limit` | int | no | default 20, cap 200 |

**Two shapes.** With no filter at all: the default read `{project, state, since[], stale, entries_since, days_since, total, verification?, note?}` - the latest state whole plus every entry after it, oldest first. With any of `since`, `kind` or `limit`: `{project, entries[], total, shown, note?}`, newest first. `verification` is `{verified, recheck, assumed, unmarked, recheck_lines[], note?}`; an unmarked line is never verified, a verified line 7 or more days old is due for a re-check.

## Not tools

These service functions exist for `pm serve` only and have no MCP twin: `Attention` (`/api/attention`, `pm today`), `ListGroups` (`/api/groups`), `Config` and `UpdateSettings` (`/api/config`, `/api/settings`), `FocusTaskSummaries` (`/api/focus`). There are no MCP resources either: each one duplicated a tool with a worse copy and bypassed the budgets, so they were removed.

## Offline twin

`pm context [project]` prints the same tracker rollup, doing list, counts and journal counts from the CLI, for when the running MCP process holds a stale binary. `pm today` is the attention block on its own; `pm today --json` is the `/api/attention` shape.
