# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
make install              # build + install (uses VERSION from Makefile)
make check                # go vet + go test
make install VERSION=X.Y.Z  # override version
```

IMPORTANT: Always use `make install` (not raw `go install`). Version is set via ldflags in Makefile. Bump `VERSION` in Makefile on each release. Current: **0.17.0**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN).

## Tests

```
go test ./internal/... -v          # all tests
go test ./internal/storage/ -v     # single package
go test ./internal/storage/ -run TestSlugify -v  # single test
```

Always add tests when implementing new features or fixing bugs. Conventions:
- Go stdlib `testing`, table-driven tests, `t.Run("case", ...)` subtests
- `t.TempDir()` for I/O tests (auto-cleanup)
- Same package tests (access to unexported functions)
- `setupTestStore(t)` helper in `store_test.go` for tests needing a Store with project + tasks
- Invariant tests (links merge, Log append, Spec rewrite, brief lifecycle) live in `tools_test.go`; the `ApplySpec`/`ExtractSpec` unit tests live in `internal/storage/spec_test.go`

## Verification

- After code changes: `make check` (runs vet + test)
- After TUI changes: `make install` + run `pm board` and test interactively
- After MCP changes: `make install` + test with `pm_context` / `pm_list_tasks` in Claude Code
- After storage changes: verify task files in `~/.claude/pm/` have correct frontmatter

## Key patterns

- **Frontmatter tasks**: `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, parent, tags, brief, ac, order, sessions, depends_on, mode) + markdown body
- **Parent/subtask rollup**: `parent` in frontmatter holds a parent task ID (e.g. `atlas-39`), making the task a child of a tracker. Link is one-way (child→parent); a task is a "tracker" iff some other task names it as parent (derived, never stored on the parent). `storage.BuildTrackers(tasks)` (in `internal/storage/tracker.go`) groups tasks by parent and returns `[]Tracker` (parent + progress map + child rollup with `brief_line`) plus a `suppressed` set (children + trackers, excluded from flat doing lists). Used by `pm_context` (MCP, `trackers` section) and `pm context` (CLI). The generated rollup replaces hand-maintained board tables in the parent body. Subtask lifecycle: `todo → doing → merged → done` (add `merged` to the project's `statuses`). Children (and the `TrackerChild.order` field) sort by `storage.LessByOrder` (Order asc, then numeric ID, then most-recently-updated) - the SAME comparator the board columns (`filteredTasks`) and the TUI tracker list (`taskChildren`) use, so all three never disagree. Set `order` via the `order` param of `pm_add_task`/`pm_update_task` (`pm_update_task` accepts a 0 to clear it), the `--order` flag of `pm add`, or `pm reorder <parent> <child-id>...` (renumbers the listed children to 10, 20, 30... in the given sequence; resolves the project by the parent's full ID, validates every ID is a real child before writing).
- **Brief field**: `brief` in frontmatter stores short session context ("where we left off"). Overwrites on each update (not append). Preserved on all status transitions (including done/archived - useful for summaries/reverts). Returned by `pm_context` for doing tasks.
- **AC field**: `ac` in frontmatter stores acceptance criteria. Overwrites on each update (same as brief). Preserved on all status transitions. Returned in `pm_context` and `pm_list_tasks` summaries. When executing a task, constrain work to AC - don't add unrequested features or deviate from stated criteria.
- **Spec / Log body zones**: the body splits into two zones with opposite write rules, solving append-only-rot on long-lived tasks. The **Spec** zone (delimited by `<!-- spec:start -->`/`<!-- spec:end -->` markers, written via the `spec` param) is the current-truth document - rewritten wholesale in place via `storage.ApplySpec(body, spec)` (`internal/storage/spec.go`). Everything outside the markers is the append-only **Log** (written via `body_append`). `storage.ExtractSpec(body)` returns the Spec content. The TUI detail view renders the markers as visible `## Spec (current truth)` / `## Log (history, append-only)` headers (`bodyToDisplayMarkdown` in `view_detail.go`). Markers default to the top of the body (Spec first, Log below); if absent when `spec` is first set, a block is prepended and the existing body becomes the Log. Canonical flow: rewrite Spec + append a one-line pointer to the Log.
- **Project config**: `~/.claude/pm/<slug>/project.yaml` — name, path, repo, stack, notes, links, tags, statuses
- **Statuses are per-project** — read via `Store.GetProjectStatuses(slug)`. Default: `[todo, doing, waiting, done]`. Override in `project.yaml`. "ALL" view merges all projects' statuses.
- **Archive is system-level** — `StatusArchived` is NOT a project status. Never add to `DefaultStatuses` or `project.yaml`. `filteredTasks()` excludes archived. `Ctrl+a` toggles archive view.
- **TUI has 4 views**: `viewBoard`, `viewDetail`, `viewArchive`, `viewProjectInfo`. `previousView` controls where detail/escape returns to.

## TUI patterns

- **`internal/tui/board` file map** (the package is split by concern; symbols moved freely, so grep by name, but for orientation): `update.go` = core `Update` dispatch + `updateBoard`; per-view key handlers in `update_picker.go` / `update_detail.go` / `update_modes.go` (select/archive/focus) / `update_menus.go` (overlays + add). Rendering: `view.go` = board render; `view_detail.go`, `view_menus.go`, `view_modes.go`. State/data: `model.go` = constructor + query/state methods; `types.go` = `view` enum + `Model` struct + item/message types; `focus.go` = focus-plan; `scroll.go` = cursor/scroll geometry; `actions.go` = status mutations + `snapshotTask`. Process launching: `launch_menu.go` (claude/executor overlay), `launch_claude.go`, `launch_codex.go`, `launch_executor.go`, `tmux.go`, `worktree.go`, `session.go`, `run_control.go` (executor run-state observe/kill), `util.go`.
- **`reload()`**: canonical way to refresh state - calls `loadTasks()` + `loadProjectCounts()` + `fixCursors()`. Don't call these separately.
- **Tab counts**: `projectCounts map[string]int` shows non-archived task count per project in tab labels.
- See `.claude/skills/tui-patterns/` for overlay menus, confirmation pattern, toast, navigation details.

## Debugging TUI layout

When fixing layout/rendering bugs, don't guess at line counts. Add temporary instrumentation and have the user screenshot:

1. **Colored section markers** - add `lipgloss.NewStyle().Background(color).Render("[TAG]")` at section boundaries to see exactly where each section starts
2. **Debug status bar** - replace the status bar with key metrics: `h` (terminal height), `curH` (computed height), `pad` (padding lines), `mch` (max card height), per-column content line counts
3. **Key formula**: `viewBoard` total visual lines = `strings.Count(output, "\n") + 1`. Must equal `m.height`. If `curH > h`, top lines get cut off by bubbletea
4. **Build with `-dbg` suffix**: use version like `0.7.1-dbg` so debug builds are clearly identified
5. **Clean up**: remove all debug code before committing. Restore `version` import if status bar uses it

Layout height budget for `viewBoard` (each item = newlines contributed):
- Title with `Padding(0,0,1,0)` + explicit `\n`: 2
- Tabs + `\n\n`: 2
- Column border/padding (`Padding(1,2)` + `RoundedBorder`): 4
- Column content (cards): `maxCardHeight` (variable)
- `\n` after columns: 1
- Confirmation line (always reserved): 1
- Status bar (no trailing `\n`): 1 visual line
- Total = `maxCardHeight + 11` (+ 1 if search/add active)

The `maxCardHeight` is computed dynamically: `m.height - overhead` where overhead accounts for all non-column lines. Column content is clamped (truncated if over, padded if under) to exactly `maxCardHeight` newlines so `JoinHorizontal` produces consistent height across projects.

## Gotchas

- `storage.DefaultStatuses` is the single source of truth for defaults - don't hardcode `[todo, doing, done]` elsewhere
- `ParseStatus` returns `TaskStatus`, not `(TaskStatus, error)` - it lowercases any string
- Task files live next to `project.yaml` in the same dir (not in a subdirectory)
- Links is `map[string]string` (freeform key=url), not a typed struct
- Version is set via ldflags at build time (`internal/version/version.go`) - defaults to "dev" without ldflags
- `cardHeight()` / `zoomCardHeight()` are estimates for reference only - not used for scroll or rendering logic. Both `fixScrollOffsets` and `viewBoard` render cards and measure actual `\n` count to handle text wrapping correctly. Column content is clamped to `maxCardHeight` as a safety net.
- **Task reorder**: `Ctrl+j/k` swaps task order within a column via `Order int` field in TaskMeta. If all tasks have `Order==0`, sequential orders are assigned first. `filteredTasks()` sorts by Order asc, then Updated desc.
- **Sessions**: `Sessions []string` in TaskMeta stores Claude session IDs (appended by TUI on launch, or via MCP `sessions` param). Used for `--resume` and `session_count` in MCP output. MCP `pm_get_task` returns full `sessions` list; `pm_update_task` and `pm_add_task` accept `sessions` param (append-only).
- **Session detection**: `pm session-id` prints current CC session UUID. Strategy: (1) walk process tree for `claude --resume <id>` arg, (2) fallback to newest .jsonl in `~/.claude/projects/<encoded-cwd>/`. Requires `CLAUDECODE=1` env var.
- **Worktree naming**: `worktreeName(t)` uses `t.Meta.Branch` if set, otherwise `Slugify(t.Meta.Title)`. Produces semantic branch names (e.g. `feat/enable-analytics` not `atlas-9`).

## Workflow

- Branch: `main` only (no feature branches for solo project)
- Commit messages: imperative, concise, prefix with feat/fix/refactor
- Always bump version in build command when releasing

## MCP server (`pm mcp`)

Stdio MCP server for Claude Code. Registered as user-scope MCP: `claude mcp add --transport stdio --scope user pm -- ~/.local/share/go/bin/pm mcp`.

- **Package**: `internal/mcpserver/` — `server.go` (setup + Run), `tools.go` (10 tools), `resources.go` (3 resources)
- **Subcommand**: `internal/cmd/mcp.go` — `pm mcp` cobra command
- **SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` — typed `AddTool[In, Out]` for auto schema generation
- **Tools**: `pm_context`, `pm_list_tasks`, `pm_get_task`, `pm_list_projects`, `pm_add_task`, `pm_update_task`, `pm_update_project`, `pm_create_project`, `pm_move_task`, `pm_delete_task`
- **Resources**: `pm://projects`, `pm://tasks/{project}/{status}`, `pm://project/{slug}`
- **CWD auto-detection**: `pm_context` with `cwd` param matches against project.yaml `path` fields
- **Trackers rollup**: `pm_context` returns a `trackers` section (parent+subtask rollup) when any task has children. Children/trackers are suppressed from the flat `doing_tasks` list. `pm_add_task`/`pm_update_task` accept a `parent` param. Same rollup is available offline via the `pm context [project]` CLI command (`internal/cmd/context.go`) - useful when the running MCP holds a stale binary.
- **Invariants**: links merge (never remove), body_append appends to the Log (never replaces), spec rewrites the Spec block in place, brief overwrites, ac overwrites, tags replace if provided, parent overwrites if provided

## Executor (`pm work` / `pm run-epic`) — autonomous task execution

Runs pm tasks/epics by spawning **isolated headless `claude -p` workers** that implement → test → review → fix → verify, so a multi-subtask epic executes without blowing one session's context. Built + validated (epic `pm-cli-18`; first real run = atlas-64-3 → PR #151, 2026-06-17).

- **`pm work [project] <task>`** (`internal/cmd/work.go`, `work_prompt.go`, `work_git.go`) — the ATOM. Spawns one fresh `claude -p` in the project dir (loads that project's skills/CLAUDE.md/MCP by cwd), runs the inner loop on one task, returns a JSON contract (`{status,summary,branch,commits,unresolved}`). Standalone → draft PR; `--epic` → commit only. `planWork` (assemble, no side effects) + `executeWork` (run + record). `--dry-run` prints the prompt+argv without spending tokens. Flags: `--model` (opus), `--max-turns` (120), `--timeout` (45m), `--yolo`, `--allow-dirty`.
- **`pm run-epic [project] <tracker>`** (`internal/cmd/run_epic.go`) — the MANAGER. Creates integration branch `epic/<tracker>`, drives ready subs sequentially (by `Order`), each on `feat/<slug>` branched off the integration branch; merges back `--no-ff` on verify-green → `done_status`. Blocked/failed/conflict → park on `waiting` + continue, and the sub's reason+open-questions bubble to the parent's `## Manager Notes` (via `recordSubFeedback`, deduped per-sub, refreshed/cleared on eventual merge). Re-entrant (skips merged/done). Ends with ONE draft `epic→main` PR. Never merges to main, never closes the parent.
- **`depends_on` gate** (`unmetDeps` in `run_epic.go`): a sub's `depends_on: [<sub-id>...]` frontmatter lists subs that must be `merged`/`done` first. The manager checks it BEFORE spawning a worker — an unmet dep skips the sub (no worker, no wasted tokens) and leaves it on its ready status (NOT parked on `waiting`), so a re-run auto-picks it up once the dep merges. A genuinely failed sub still goes to `waiting` (needs a fix + flip back to `todo`); a dep-skip is just "not its turn yet". Opt-in (empty = run by `Order`). Settable via `pm_add_task`/`pm_update_task` `depends_on` param; shown in `pm run-epic --dry-run`.
- **`mode: auto | manual` gate** (`classifySub` in `run_epic.go`): a sub's `mode` frontmatter marks it autonomous (`auto`/empty, default - backward compatible) or human-only (`manual`). A `manual` sub is a PERMANENT gate: the manager skips it entirely (no worker spawned, status untouched) on EVERY run, records a distinct `manual` outcome (not `skipped`), and never re-picks it - unlike the re-entrant `depends_on` skip. The human does the work by hand / with CC and moves the sub to `done_status` themselves; only then does the re-entrant done check pick it up. Use for subs needing interactive/visual work (simulator verification, design checks) that a headless `claude -p` can't do. A later auto sub with a `manual` sub in its `depends_on` stays blocked via the normal `unmetDeps` gate until the human moves the manual sub to done (no special-case logic). Only `auto`/`manual` (lowercase) are valid - `storage.ValidateMode` rejects anything else at write time. Settable via `pm_add_task`/`pm_update_task` `mode` param; shown as `[MANUAL]` in `pm run-epic --dry-run`. No CLI flag - it's a per-task frontmatter field.
- **Profile** (`internal/storage/executor.go`): an `executor` block in `project.yaml` binds each phase (`implement/test/review/verify/pr`) to `skill:` | `cmd:` | empty (built-in generic) | `false` (skip). No block = all generics, `additional_worktree:false`. `pm executor init <project>` (`internal/cmd/executor_init.go`) auto-detects skills+stack and drafts it.
- **Autonomy envelope** (hard rules, in `buildWorkerSystemPrompt`): stay within AC (no scope creep), never merge to main / force-push / hard-reset, commit on the given branch only. Permissions: `acceptEdits` + curated bash allowlist + disallow force-push/merge/hard-reset (`--yolo` bypasses).
- **Worker = `claude -p`**, NO git worktree by default. Headless workers CANNOT use interactive-auth MCP (e.g. figma) → design-heavy tasks need a manual visual check.
- **Additional worktree mode** (`internal/storage/worktree.go` + `internal/cmd/work_worktree.go`): opt-in PER RUN via the `--additional` flag on `pm work`/`pm run-epic` - NOT a global project setting. Default (no flag) = the main checkout with the clean-tree precondition, i.e. exactly the pre-worktree behaviour; nothing changes for a run that doesn't ask for it. `executor.additional_worktree: true` in project.yaml means "the additional worktree is CONFIGURED/available for this project" (a capability gate that makes `worktree_path`/`base_branch`/`env` meaningful), not "always use it"; `--additional` on a project without it errors ("no additional worktree configured - set executor.additional_worktree: true..."). When `--additional` is passed, the worker runs in ONE fixed "additional" git worktree (path from `worktree_path`, default `<repo>-additional`), leaving the user's default checkout untouched. Single deliberate slot - NOT an N-slot/port-registry mechanism (by design; extend later if 2 proves too few). The board offers it as a per-launch `#` toggle in the executor launch overlay (shown only when the project has it configured; alongside the `!` yolo toggle) - the user picks default vs additional every time, never inferred. `--dry-run` (CLI and board) prints the chosen mode (`run: DEFAULT ...` / `run: ADDITIONAL worktree ...`). Flow when additional is on (`prepareWorktree`): `EnsureWorktree` (create if absent, REUSE if present so installed `node_modules`/`Pods` survive between runs; a non-worktree path is refused), seed untracked configs via the SAME `storage.CopyUntrackedFiles` the TUI worktree feature uses (`git ls-files --others` - copies gitignored `.env*`/plists, never clobbers existing), `ExcludeFromGit` the lock file, then a PID lock. `workDir` (worktree path, else `proj.Path`) is the worker cwd AND the target of every git op; `RunState.RepoPath = workDir` so the TUI resolves the transcript. pm-cli stays project-agnostic: it knows nothing about RN/Metro/simulators - `yarn install`/`pod install`/boot/build are the worker's job, driven by the project's own skills/CLAUDE.md.
  - **Lock** (`.pm-executor.lock` at the worktree root, JSON `{pid,task_id,kind,started}`): `AcquireWorktreeLock` refuses with `*WorktreeBusyError` ("additional worktree busy (pid X, task Y)...") when a LIVE process holds it; a stale lock (dead holder pid) is taken over automatically; same-pid re-acquire is idempotent (epic manager re-enters per sub). Held for the whole run (one task for `pm work`, the whole epic for `run-epic`), released by a deferred closure on normal exit. Kept out of `git status` via `.git/info/exclude`, and the dirty-tree precondition is skipped in worktree mode (the worktree is executor-managed; the board's `boardGitPreflight` also drops the clean-tree check so a dirty main checkout doesn't block a launch). A SIGTERM kill can't run the defer, so `killRun` releases the lock (PID-matched) on the manager's behalf, and stale-recovery is the backstop.
  - **Env injection**: `executor.env` is an opaque `string->string` map (e.g. `ADDITIONAL_METRO_PORT`, `ADDITIONAL_SIM_UDID`) appended verbatim to the worker's environment (`executorEnvSlice` → `runWorker` extraEnv, wins over inherited). pm does NOT interpret these - project-specific knowledge (ports, UDIDs) lives in `project.yaml`, not pm.
  - **Fresh branch per task on the reused worktree** (`gitCleanWorktree` + `gitFreshBranch` in `work_git.go`): because "additional" is reused across many tasks, each new unit of work gets a branch forked from its base's CURRENT tip on a wiped-clean tree - it never continues the previous task's branch nor inherits a killed run's leftovers. `gitCleanWorktree` = `git reset --hard` + `git clean -fd` (NO `-x`, so ignored deps/configs - node_modules/Pods/`.env`/the lock - survive; only uncommitted tracked changes + untracked non-ignored source are dropped). `gitFreshBranch` = clean + `git checkout -B <branch> <base>` (`-B` force-resets even if the branch lingers from a prior killed attempt). Only in worktree mode - the non-worktree path keeps its create-or-continue `gitCheckoutBranch`. `pm run-epic`: cleans once at epic start before the integration branch (preserving its COMMITTED re-entrant history), and each sub's `feat/<slug>` is a `gitFreshBranch` off the integration tip.
  - **Base branch precedence** (`resolveWorktreeBase`, only under `--additional`): the base the fresh task/epic branch forks from is `--base` flag > `executor.base_branch` (project.yaml) > fallback. Fallback is the main checkout's current branch for `pm work` and `main` for `pm run-epic`. Set `base_branch: development` in the executor block so an additional run never accidentally forks off whatever the user's main checkout happens to have checked out; both `--base` flags default to empty (unset) so the config can win. In DEFAULT mode `base_branch` is NOT consulted (run-epic falls back to `--base` or `main`, matching pre-worktree behaviour). `pm work --additional --dry-run` prints the resolved `fresh branch base`.
- **Worker env** (`workerEnv` in `work.go`): strips `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` so the headless `claude -p` authenticates via the Claude **subscription**, not the metered API — the executor spawns many workers and the API path would bill per-token (see memory `reference_claude_p_billing`).
- **Observability = pm is the bus** (`internal/storage/executor_run.go`): each run writes a `RunState` JSON + `.log` under `<project>/.executor/<taskID>.json` (`WriteRunState`/`ReadRunState`; `RunState` carries PID, RepoPath, CurrentSub/Session, Phase, and per-sub `[]SubRun` with status `pending|running|merged|blocked|failed|conflict|skipped`). The TUI is the read side: a run **dashboard** renders in the detail view, `W` opens the live **agent-view** (tails the worker's transcript `.jsonl` via CurrentSession), and `refreshRunStates` repolls on each tick. Runs launched from the board's executor overlay are **detached** (`Setsid`, survive board exit) and show a `▶` badge. `RunState.IsLive()` = status `running` AND `ProcessAlive(PID)` (signal-0 check).
- **Kill a live run** (`K` → confirm; `killRun` + `RunState.Kill` in `run_control.go`): group-kills the manager+worker tree (SIGTERM to `-pgid`, escalates to SIGKILL ~2s later if still alive), stamps the run-state `failed`, and parks the in-flight sub on `waiting` so the board reflects the stop.
- **Executor journal** (`internal/storage/executor_journal.go`): append-only JSONL history of executor runs at `<project>/.executor/journal.jsonl` - the durable cross-run record (RunState is the LIVE state of one run, overwritten in place; the journal is never rewritten). Event model: `start` line when a run begins, `end` line with per-sub outcomes (`result`/`note`/`duration_s`/`session`) + run duration when it finishes, `killed` line appended by the board's `killRun` on the dead manager's behalf; a `start` with no `end`/`killed` = untracked crash. Written by `pm run-epic` (manager level) and standalone `pm work` (epic subs are journaled by the manager, not per-worker). All appends best-effort like `WriteRunState`. `ReadJournal` skips corrupt lines. Consumed by the `/executor-retro` skill to mine real-run history (fail rates, park reasons, durations) for epic-flow improvements.
