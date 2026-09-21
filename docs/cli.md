# CLI reference

Every command the `pm` binary exposes, with its flags and defaults, derived from `internal/cmd`. This is for people running pm from a shell or a script; an agent normally goes through the MCP server instead ([mcp.md](mcp.md)). `pm help <command>` and `pm completion <shell>` come from cobra, and `--help` prints any command's flags. A bare `pm` opens the board.

Argument conventions: `<project>` is a project slug (a case-folded prefix works when it is unique), `<task-id>` is a full task id such as `acme-12` unless the command says it searches. `[project]` in brackets means the project is optional and is detected from the current directory when omitted, by matching cwd against each project's `path`.

## Tasks

### pm add <project> <title>

Create a task file in the project directory and print its id and path. Takes the project lock, so a concurrent board or MCP write cannot mint the same id. The status must be one of the project's statuses.

| flag | default | meaning |
|---|---|---|
| `--status` | `todo` | initial status |
| `--id` | auto | explicit task id (must be a safe file-name component) |
| `-l`, `--link key=url` | | links, repeatable |
| `--branch` | | git branch name |
| `--tag` | | tags, repeatable |
| `--order` | `0` | sort order within a column or a parent's rollup; lower first, convention 10, 20, 30 |

### pm list (alias: ls)

Print a `STATUS PROJECT ID TITLE` table of every non-archived task, filtered in-process. Text only.

| flag | default | meaning |
|---|---|---|
| `-p`, `--project` | | only this project |
| `-s`, `--status` | | only this status |

### pm show <project> <task-id>

Render one task through glamour: a header with status, updated, "since" (from `status_changed`), `waiting_for`, branch, links and tags, then the body. Falls back to raw text when rendering fails.

### pm mv <project> <task-id> <status>

Move a task to another status. The status is validated against the project's set (`archived` is always legal); the move stamps `status_changed` only when the status actually changes.

### pm done <project> <task-id>

`pm mv ... done`.

### pm edit <project> <task-id>

Open the task file in `$EDITOR` (fallback `nvim`).

### pm reorder <parent> <child-id>...

Renumber the listed children's `order` to 10, 20, 30 in the given sequence, so `pm context` and the board show them that way. Unlisted children keep their order. The parent is resolved by full id across every project; every listed id must be a child of it.

## Projects and setup

### pm init

Create the data directory (`$PM_DATA_DIR`, else `~/.claude/pm`). It creates no project.

### pm projects (alias: proj)

List every project with its per-status task counts and stack.

### pm projects add <slug>

Create `<data dir>/<slug>/project.yaml`. The slug must be a safe directory name (lowercase letters, digits, `.`, `_`, `-`); a rejected slug gets a suggested slugified form. `--path` matters: cwd auto-detection matches against it, so a project without one is never found from its own checkout.

| flag | default | meaning |
|---|---|---|
| `--path` | | local repo path |
| `--repo` | | remote repo URL |
| `--name` | slug | display name |
| `--stack` | | tech stack summary |
| `--notes` | | free notes |
| `--tag` | | tags, repeatable |

### pm projects edit <slug>

Open `project.yaml` in `$EDITOR` (fallback `nvim`).

### pm config

Group command for the global `config.yaml` (remote runners, the cockpit block). Prints help.

### pm config show

Print the resolved global config and the file it came from: every remote runner and the cockpit block with defaults filled in. No file means zero remotes and the defaults, which is normal; an unreadable file is an error.

### pm docs

Print the embedded agent docs. `pm docs claude` prints the usage contract shaped as a CLAUDE.md block between `<!-- pm:agent-guide:start/end -->` markers (install with `pm docs claude >> ~/.claude/CLAUDE.md`); `pm docs authoring` prints the task-authoring rules. Both are versioned in `docs/agent-guide.md` and `docs/task-authoring.md`.

### pm session-id

Print the Claude Code session UUID of the session this command runs in, with no trailing newline. Reads `CLAUDE_CODE_SESSION_ID`, else walks the process tree for a `claude --resume <id>` argument, else takes the newest session file under the Claude config dir. Refuses unless `CLAUDECODE=1`.

## Reading

### pm board [project]

Open the kanban TUI ([board.md](board.md)), on the given project or the one detected from cwd. Same as a bare `pm`.

### pm context [project]

Print the session-start rollup an agent reads: every tracker with its children's status marks, standalone doing tasks, counts, and pointer lines only for the executor profile (`pm executor show`) and the journals (`pm journal show`). This is the offline twin of the `pm_context` MCP tool, useful when the running MCP holds a stale binary.

### pm today

Print the attention queue, the cockpit's home screen as text: the sections in a fixed order (`needs_me`, `solo_reports`, `landed_no_pr`, `focus`, `in_progress`, `waiting`, `changes`, `stuck_projects`, plus the opt-in `new_since_cutoff` and `recent`; `needs_me`, `landed_no_pr` and `in_progress` only with `cockpit.show_executor` on), one line per row with the reason and the age. Thresholds come from the `cockpit:` block of the global config.

| flag | default | meaning |
|---|---|---|
| `--json` | off | emit exactly what `pm serve` answers on `/api/attention` |
| `--project` | | only this project's rows |
| `--group` | | only this group's rows (every member project) |

### pm runs

One row per tracker across every project: its run state (`prepped`, `running N/M`, `done N/M`, `failed N/M`, `stale N/M`), its acceptance state (`-`, `running (host, age)`, `done`, `partial`, `blocked`, `failed`, `stale`) and open visual claims. Remote runners from `config.yaml` are queried over ssh; one that cannot be reached costs a single note row, never the local rows and never a non-zero exit. `--local` and `--remote` together is an error.

| flag | default | meaning |
|---|---|---|
| `--json` | off | emit `{"rows": [...]}` instead of the table |
| `--local` | off | this machine only, contact no remote runner |
| `-p`, `--project` | | only this project (a remote-only project cannot be named) |
| `--remote <name>` | | only this remote runner |

### pm journal

Per-project record of a repeatedly-troublesome subsystem ([journals-and-timeline.md](journals-and-timeline.md)). Which subsystems a project journals is declared in `project.yaml` under `journals:`; an undeclared name is an error, never an implicit create. A bare `pm journal` is `pm journal list`. Persistent flag on the group, inherited by every subcommand:

| flag | default | meaning |
|---|---|---|
| `-p`, `--project` | cwd | project slug |

### pm journal list

The journals declared for the project, each with its entry count and open count.

### pm journal show <name>

Entries newest first: date, id, symptom, the wrong conclusion, cause, fix (or `OPEN`, or "closed by a later entry"), what it closes, minutes and tags.

| flag | default | meaning |
|---|---|---|
| `--limit` | `20` | max entries (0 = all) |
| `--open` | off | only entries with no fix recorded |

### pm journal add <name>

Append an entry. Leave `--fix` empty while the fix is not made; that is what open means. `--resolves` closes earlier open entries by id and an unknown id is rejected before the write.

| flag | default | meaning |
|---|---|---|
| `--symptom` | required | what was observed |
| `--false` | | the wrong belief the symptom produced |
| `--cause` | | the real cause |
| `--cost` | `0` | minutes lost |
| `--fix` | | the rule it produced (empty = open) |
| `--tag` | | tags, repeatable |
| `--session` | | Claude session id |
| `--resolves` | | ids of earlier entries this one closes, repeatable |
| `--date` | today | back-date the entry (`YYYY-MM-DD`) when seeding history |

### pm journal stats <name>

Totals, open and closed-later counts, cost (averaged over the entries that recorded one), per-month bars, per-tag clusters and the open list, all derived from the entries.

### pm timeline [project]

What happened to a project and where it stands: the latest `state` entry in full plus every entry written after it, oldest first, then a `stale:` line after 10 entries or 7 days. State lines ending in `[verified YYYY-MM-DD by <source>]` or `[assumed]` are counted under a `verification:` line, with a ` ! ` gutter on verified lines due for a re-check and ` ~ ` on assumed ones. The positional project beats `--project`, which beats cwd.

| flag | default | meaning |
|---|---|---|
| `-p`, `--project` | cwd | project slug (persistent, inherited by `add` and `list`) |
| `--json` | off | print the default read as one JSON object |

### pm timeline add

Append an entry. `--kind` is one of `event`, `decision`, `state`; `--text -` reads stdin, which is how a multi-line state gets in. A `[verified]` marker with no day or a future day is refused. Entries are never edited; a changed picture is a new state.

| flag | default | meaning |
|---|---|---|
| `--kind` | required | `event`, `decision` or `state` |
| `--text` | required | the entry; `-` reads stdin |
| `--ref` | | a task id, path or URL stored verbatim, repeatable |
| `--session` | detected | Claude session id, detected when run inside Claude Code |
| `--date` | now | back-date (`YYYY-MM-DD`): midnight of that day, except today, which takes the current time |

### pm timeline list

Entries newest first across every month.

| flag | default | meaning |
|---|---|---|
| `--since` | | only entries on or after this day (`YYYY-MM-DD`) |
| `--kind` | | only this kind |
| `--json` | off | `{entries, matched, total}` |

## Agents

The executor commands are documented end to end in [executor.md](executor.md); this section lists the flags. Each of `work`, `run-epic` and `finish` prints its result envelope as JSON on stdout, without a flag. Solo has no command of its own: a shift is launched from the cockpit's Runs screen and read back through `pm today` and the cockpit, see [solo-and-batch.md](solo-and-batch.md).

### pm work [project] <task-id>

Spawn one fresh headless `claude -p` worker in the project directory to run the inner loop (implement, test, review, fix, verify) on one task under the project's executor profile. Standalone it ends with a draft PR; with `--epic` (the manager calls it this way) it commits on the current branch with no per-task PR. With one argument the task is found across projects in three tiers (exact id, unique id prefix, title substring), cwd breaking ties inside a tier; a cross-project tie is an error listing the candidates.

| flag | default | meaning |
|---|---|---|
| `--epic` | off | commit on the current branch, no PR |
| `--dry-run` | off | print the worker prompt and command without invoking claude |
| `--model` | `opus` | worker model; beats the task's `model:` and `executor.model` |
| `--effort` | | Claude effort level (`low`, `medium`, `high`, `xhigh`, `max`); beats `executor.effort` |
| `--max-turns` | `150` | max agent turns |
| `--yolo` | off | bypass all permission checks instead of the curated allowlist |
| `--allow-dirty` | off | skip the clean-working-tree precondition |
| `--timeout` | `120m` | wall-clock limit before the worker is killed; overrides `executor.timeout` |
| `--base` | | with `--additional`: the base the task branch forks from (default `executor.base_branch`, else the main checkout's branch) |
| `--additional` | off | run in an isolated worktree slot; requires `executor.worktrees` (or the legacy `additional_worktree`) in `project.yaml` |
| `--slot` | `0` | with `--additional`: pin a slot (1-based); 0 = first free |

### pm run-epic [project] <tracker-id>

Drive a parent's subtasks through workers, one after another. Integration mode (the default): an `epic/<tracker>` branch, one `feat/<slug>` branch per sub merged back on green, blocked subs parked with a reason, one draft PR from the epic branch at the end; re-entrant, so finished subs are skipped. Independent mode (`epic_mode: independent` on the tracker, or `--independent`): each sub on its own branch off the base, pushed when it carries commits, nothing merged, no epic PR. The task is resolved like `pm work`.

| flag | default | meaning |
|---|---|---|
| `--model` | `opus` | worker model; a sub's own `model:` beats it |
| `--effort` | | effort level for the workers |
| `--max-turns` | `150` | max agent turns per worker |
| `--yolo` | off | bypass permission checks in the workers |
| `--timeout` | `120m` | wall-clock limit per worker |
| `--base` | | the branch the integration branch forks from (default `executor.base_branch` with `--additional`, else `main`) |
| `--no-pr` | off | do not open the final draft PR |
| `--allow-dirty` | off | skip the clean-working-tree precondition |
| `--dry-run` | off | print the plan (subs, branches, readiness) and stop |
| `--additional` | off | run the whole epic in an isolated worktree slot |
| `--slot` | `0` | with `--additional`: pin a slot |
| `--independent` | off | batch mode, see above |
| `--then-finish` | tracker's `finish_mode` | spawn a detached `pm finish <tracker>` when the run ends; `--then-finish=false` suppresses a tracker's `finish_mode: auto` |

### pm finish [tracker]

The acceptance of an executor run: a headless worker that runs the acceptance procedure for the tracker, wrapped in the same harness (claim, run-state, guard hook, journal, report). The claim lives at `<project dir>/.executor/<tracker>.finish.claim`, next to the run rather than on the accepting machine, and is valid for a TTL. Without a tracker it prints help. `--sim` and `--no-sim` together is an error. Persistent flag inherited by the four subcommands:

| flag | default | meaning |
|---|---|---|
| `-p`, `--project` | cwd | project slug |

Local flags:

| flag | default | meaning |
|---|---|---|
| `--sim` | off | let the acceptance use the simulator or runtime (only with a human present) |
| `--no-sim` | the default | never touch the simulator; visual claims come back counted, not guessed |
| `--no-yolo` | off | run under the curated allowlist instead of bypassing permission prompts |
| `--dry-run` | off | print the acceptance prompt and command; claims nothing |
| `--model` | `opus` | acceptance worker model |
| `--max-turns` | `300` | max agent turns |
| `--timeout` | `120m` | wall-clock limit |
| `--additional` | off | run in an isolated worktree slot |
| `--slot` | `0` | with `--additional`: pin a slot |

### pm finish claim <tracker>

Claim a run for acceptance. Fails with a non-zero exit when somebody else holds it; re-claiming with the same `--session` refreshes instead. Prints the claim path, the `started` stamp and the release command.

| flag | default | meaning |
|---|---|---|
| `--session` | | session or run id recorded in the claim; also the refresh identity |

### pm finish release <tracker>

Release a claim you can identify as yours. An expired claim clears without an identifier; a live claim on another host refuses; no claim at all is a no-op.

| flag | default | meaning |
|---|---|---|
| `--started` | | the claim's `started` stamp as printed by `pm finish claim` |
| `--session` | | the session id recorded in the claim |

### pm finish status <tracker>

Print who holds the claim and since when: free, free (expired), or claimed with host, pid, started, refreshed and session.

### pm finish record <tracker>

Record a hand-driven acceptance's verdict as the same `<tracker>.finish.json` a headless run leaves, so the runs table keeps it after the claim lapses. Refuses when someone else holds a live claim; does not release yours; re-running overwrites, except over a live headless acceptance. Writes no executor-journal lines.

| flag | default | meaning |
|---|---|---|
| `--result` | required | `done`, `partial` or `blocked` |
| `--claims-open` | `0` | visual claims still awaiting a human |
| `--note` | | free text |
| `--session` | | session id |
| `--report` | | path to a markdown report, saved as `<tracker>.finish.md` (an empty file is rejected) |

### pm executor

Executor profile tooling. Prints help.

### pm executor init [project]

Draft the `executor:` block of `project.yaml` by scanning the project's `.claude/skills` and `.claude/commands` and its stack. Over an existing block it refreshes `phases` and `baseline`, fills `handoff` only when empty, and keeps every hand-set field. Requires `path` on the project.

| flag | default | meaning |
|---|---|---|
| `--dry-run` | off | print the drafted profile without writing |

### pm executor show [project]

Print the resolved profile: worktree slot paths expanded with env merged, context repos, phase bindings and the handoff contract with the runtime skill's scripts read from disk.

### pm executor doctor [project]

Check the profile for completeness, not YAML syntax: the declared playbook and runtime skill exist, context repos and slot paths are usable, the playbook mentions every script the runtime skill ships and hardcodes no runtime ids. Exit 1 on an error, 0 on warnings.

| flag | default | meaning |
|---|---|---|
| `--strict` | off | treat warnings as failures |

### pm executor stats [project]

Summarise `<project dir>/.executor/journal.jsonl`: runs per kind and terminal status, detected crashes, the sub outcome histogram, duration, turns and cost totals with averages, plus review telemetry. Read-only; an empty journal prints a note.

### pm serve

Serve the web cockpit ([cockpit.md](cockpit.md)): the JSON API under `/api/`, the SSE feed at `/api/events`, the change feed's scheduler and the React front end built into the binary (a placeholder page until `make web` has run). No auth, no TLS; POSTs need the `X-PM-Client` header. SIGINT or SIGTERM shuts it down within five seconds.

| flag | default | meaning |
|---|---|---|
| `--addr` | `127.0.0.1:7070` | address to listen on |

### pm mcp

Start the MCP server on stdin/stdout for Claude Code ([mcp.md](mcp.md)). Register it once with `claude mcp add --transport stdio --scope user pm -- pm mcp`.

## Hidden commands

- `pm migrate-ids` - one-shot legacy migration to sequential `<prefix>-<n>` ids; refuses when any task carries `parent` or `depends_on`.
- `pm worker-guard` - the PreToolUse hook pm attaches to every executor worker (`--telemetry`, `--diff-base`, `--review-model`, `--fix-rounds`); reads a Claude Code hook payload on stdin and exits 2 to deny. Not meant to be run by hand.

## --json

| command | shape |
|---|---|
| `pm today --json` | the `/api/attention` object |
| `pm runs --json` | `{"rows": [...]}`, never `null` |
| `pm timeline --json` | the default read (`state`, `since`, `stale`, `verification`, ...) |
| `pm timeline list --json` | `{entries, matched, total}` |

`pm work`, `pm run-epic` and `pm finish` always print their result envelope as indented JSON; there is no flag. Every other command prints text.

## Environment variables

Read by pm:

| variable | effect |
|---|---|
| `PM_DATA_DIR` | the data root; default `~/.claude/pm`. Also where `config.yaml` is looked up |
| `EDITOR` | editor for `pm edit` and `pm projects edit`; fallback `nvim` |
| `CLAUDECODE` | `1` means "running inside Claude Code": `pm session-id` refuses otherwise, `pm timeline add` only auto-detects a session then |
| `CLAUDE_CODE_SESSION_ID` | the authoritative session id for `pm session-id` |
| `CLAUDE_CONFIG_DIR` | an alternate Claude config dir; the session-file fallback looks there |
| `TMUX` | board only: whether the tmux launch variants are offered |
| `GH_TOKEN` | passed to `gh` by the change feed and the review flow when set |

Set by pm on the processes it spawns:

| variable | effect |
|---|---|
| `PM_HEADLESS=1` | on every executor worker, so a project's session hooks can skip interactive-only setup |
| `PM_LAUNCH_SOURCE` | who launched a run (the cockpit sets `cockpit`); stamped into the executor journal |
| `CLAUDE_CONFIG_DIR` | pinned on a worker when the project's `worker_claude_config_dir` differs from the default |
| `GH_PAGER=cat`, `PAGER=cat`, `GIT_TERMINAL_PROMPT=0`, `GH_PROMPT_DISABLED=1`, `NO_COLOR=1` | on every `git` and `gh` call the change feed makes, so nothing pages or prompts |

Stripped from every worker: `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`. A worker bills the subscription, never the API.

## External binaries

| binary | used by |
|---|---|
| `claude` | `pm work`, `pm run-epic`, `pm finish`; the board's launch menu |
| `git` | the executor (branches, merges, worktrees, pushes), `pm executor doctor`, the review flow |
| `gh` | `pm run-epic` (the draft PR, unless `--no-pr`), the change feed, standalone workers' draft PRs |
| `ssh` | `pm runs` and `pm serve`'s remote-runs fetch: `<remote.pm> runs --json --local` with a 5 s connect timeout and a 20 s cap |
| `sh -c` | the profile's `prepare`, `baseline` and `rig` commands |
| `ps` | `pm session-id`'s process-tree walk |
| `$EDITOR` | `pm edit`, `pm projects edit` |
| `tmux` | board launches only |
| `codex` | the board's third launch agent |
| `pm` itself | `pm run-epic --then-finish` re-execs `pm finish <tracker> --project <slug> --no-sim` detached |

Commands that can outlive their parent (`claude`, `ssh`, the `sh -c` hooks, the feed's `git` and `gh`) run in their own process group and get SIGTERM then SIGKILL when their context ends.

## Version

`pm --version` reports, in this order of precedence: the value stamped through ldflags by `make install` (`VERSION` in the Makefile), else the Go build info's module version (a tag for `go install ...@v0.65.0`, a pseudo-version for an untagged commit, `(devel)` for a working-tree build), else `dev`. It is never empty, because cobra would otherwise drop the `--version` flag.
