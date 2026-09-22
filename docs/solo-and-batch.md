# Solo and batch: working while nobody watches

pm has two ways to get work done without a person at the keyboard. **Solo** is
one long-lived Claude Code session working a queue of tasks in the project's
own checkout, launched from the cockpit; it is the current path. The
**executor** (`pm work`, `pm run-epic`, `pm finish`) runs headless `claude -p`
workers, one fresh process per task, and its **independent mode** is what a
batch of unrelated tickets runs through. The executor is frozen in favour of
solo since 2026-09-09: the cockpit hides its runs unless `cockpit.show_executor`
is on ([data-model.md](data-model.md), the `cockpit:` block), while the CLI and
the board still run all of it.

Both paths hand the *procedure* to a skill that is **not in this repository**:
solo runs the `/solo` skill, and `pm finish` tells its worker to invoke the
`batch-finish-auto` skill (`internal/cmd/finish_prompt.go`). pm supplies the
launch, the process harness, the files and the screens; the skills live in
[mbalazy/claude-skills](https://github.com/mbalazy/claude-skills), linked into
`~/.claude/skills` of the machine that runs them. This page is the manual for
what pm does; the executor's internals are in [executor.md](executor.md).

Install the skills once, before the first shift - `install.sh` links them into
`~/.claude/skills` (or the config dir in `CLAUDE_CONFIG_DIR`, for a project
pinned to another one):

```sh
git clone https://github.com/mbalazy/claude-skills
./claude-skills/install.sh
```

`pm executor doctor <project>` warns while `solo` or `batch-finish-auto` is
missing under the project's Claude config dir, and the launch form's preview
carries the same warning.

## Solo

### Starting a shift

Open the cockpit's Runs screen (`/runs`, key `3`) and fill the "launch solo"
form: the project, the queue, and the flags. The queue is handed to the skill
verbatim - a tracker id, task ids, a ticket key, a link or pasted text. The
flags are the skill's own:

| field | flag |
|---|---|
| runtime | `--sim`, `--web` or `--no-runtime`; empty lets the skill decide |
| push / PR | `--push`, `--pr` |
| limits | `--max-tasks N`, `--max-hours H` |
| base | `--base <branch>`; empty = the skill's default |
| model | `--model <alias>`; empty = the account's default |

The confirmation dialog shows the exact command that will run - the preview
is the argv, byte for byte - and the warnings the launcher found. Warnings
never block: `claude` not on `PATH`, `permissions.ask` rules in the repo's
`.claude/settings.json` without a `settings.local.json` deny mirror, a dirty
tree, a missing base or origin ahead of it, an open `.shift/` directory, a
live launch of the same project.

The session is a Claude Code **background session** (`claude --bg`, Claude
Code 2.1.272 or newer) started in the project's main checkout, under Claude
Code's own supervisor rather than under `pm serve`. It runs with permissions
bypassed, a 400k auto-compact window and `--setting-sources user,local` when
the repo's shared settings carry `ask` rules. The launch environment drops
`ANTHROPIC_API_KEY` (the subscription pays) and every `CLAUDE_CODE_*`
variable of the shell `pm serve` was started from, and pins the project's
`claude_config_dir`.

### Joining, watching, stopping

The Runs table shows every launch with its state, the attach command with a
copy button, and a stop button. From any terminal:

```sh
claude attach <id>     # jump into the session; Ctrl+Z detaches
claude logs <id>       # read it
claude stop <id>       # end it - the only way: a process killed by a signal is restarted by the supervisor
```

When the project uses a non-default `claude_config_dir`, the attach command
in the table carries the `CLAUDE_CONFIG_DIR=<dir>` prefix it needs. The
states are the supervisor's words - `working`, `blocked`, `done`, `failed`,
`stopped` - plus pm's own for the moments it has no row: `starting` (just
launched), `unknown` (no row and not fresh), `error` (the launch itself
failed). A launch survives a `pm serve` restart: its record is
`<pm-root>/.cockpit/solo/<id>.json` with the launcher's output in `.log`.

### What pm reads back

The skill keeps one state file per shift at
`<project dir>/.shift/<YYYY-MM-DD>-<id>.md` and writes the closing report
next to it as `<...>-report.md`. The format is the skill's prose, not a pm
contract, so pm reads it leniently: the header, the status line, the queue
with each task's status at shift start, each task's outcome (`done`,
`parked`, `untouched`, `doing`) and branch. Whatever does not parse stays
empty, never guessed.

Where it shows up:

- `/runs` lists the shifts first, launches joined to them by session id.
- `/solo/<project>/<shift>` is the report as a digest, with "mark read".
- Today's `solo_reports` section carries every shift closed within the last
  14 days that left a report; a row can be dismissed, and comes back on its
  own when a new condition appears. `pm today` prints the same section as
  "solo reports".
- The API: `GET /api/solo` (shifts and launches), `GET /api/solo/plan`
  (the preview), `POST /api/solo` (launch), `GET /api/solo/{id}`,
  `POST /api/solo/{id}/stop`, `GET /api/solo/{project}/{shift}/report`.

There is no `pm solo` command: the launch is the cockpit's, the reading is
`pm today` and the screens above.

## Batch: `pm run-epic` in independent mode

A batch is a tracker of unrelated subtasks run one after another, each on its
own branch, nothing merged, and accepted afterwards by a separate run. The
recipe:

1. **Prepare the tracker.** A parent task with `epic_mode: independent`, and
   one sub per ticket carrying `parent`. On a sub, `order` sets the sequence,
   `depends_on` makes the manager skip it until the named subs have landed,
   `mode: manual` keeps the manager off it entirely, `model` picks a cheaper
   or stronger worker for that sub alone. `finish_mode: auto` on the tracker
   chains the acceptance after the run; `--then-finish` does the same per
   run. The fields and their write rules: [data-model.md](data-model.md).
2. **Inspect, then run.**
   ```sh
   pm run-epic <tracker> --dry-run   # the plan: subs, branches, readiness; zero tokens
   pm run-epic <tracker>             # the run; --independent is the flag form of epic_mode
   pm run-epic <tracker> --additional   # the same in an isolated worktree slot
   ```
   Every flag is tabled in [cli.md](cli.md).
3. **What each sub gets.** A fresh branch off the base, a worker that runs
   best-effort - it never abandons the task; it records `ASSUMPTION:`,
   `TODO:` and `SPEC-CONFLICT:` entries instead - and a push whenever the
   branch carries commits, even after a failure. A green sub lands on the
   project's `done_status_independent` status, `pushed` by default. Nothing
   merges and there is no epic PR; the run is re-entrant, so a re-run skips
   finished subs.
4. **Accept.** `pm finish <tracker>` (or the auto-chain) claims the run - a
   TTL lock at `<project dir>/.executor/<tracker>.finish.claim`, next to the
   run rather than on the accepting machine, so two acceptances can never
   take the same run - and spawns a headless worker that invokes the
   `batch-finish-auto` skill: walk the sub branches, verify, push fixes,
   write a report. `--no-sim` is the default and a detached acceptance keeps
   its hands off any shared simulator or runtime; `--sim` is for a run with a
   person present. Its state, log and report are
   `.executor/<tracker>.finish.json`, `.finish.log` and `.finish.md`.
5. **Read the result.** `pm runs` is one row per tracker across every
   project: the run state, the acceptance state and the count of open visual
   claims, remote runners included; the board's `R` view shows the same.
   With `cockpit.show_executor` on, the executor's three Today sections come
   back: `needs_me` (a run that failed or crashed, an acceptance that failed,
   open visual claims, a run done with no acceptance), `landed_no_pr` and
   `in_progress`. The acceptance's written report is `.finish.md`.

### Not a batch: integration mode

`pm run-epic` without `epic_mode: independent` is the other mode, for ONE
coherent feature: an `epic/<tracker>` branch, each sub merged back onto it on
green, blocked subs parked on `waiting`, one draft PR from the epic branch at
the end. Both modes, the worktree slots, the review policy and the verification
baseline are in [executor.md](executor.md).
