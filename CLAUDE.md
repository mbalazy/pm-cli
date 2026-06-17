# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
make install              # build + install (uses VERSION from Makefile)
make check                # go vet + go test
make install VERSION=X.Y.Z  # override version
```

IMPORTANT: Always use `make install` (not raw `go install`). Version is set via ldflags in Makefile. Bump `VERSION` in Makefile on each release. Current: **0.9.0**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN).

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

- **Frontmatter tasks**: `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, parent, tags, brief, ac, order, sessions) + markdown body
- **Parent/subtask rollup**: `parent` in frontmatter holds a parent task ID (e.g. `atlas-39`), making the task a child of a tracker. Link is one-way (child→parent); a task is a "tracker" iff some other task names it as parent (derived, never stored on the parent). `storage.BuildTrackers(tasks)` (in `internal/storage/tracker.go`) groups tasks by parent and returns `[]Tracker` (parent + progress map + child rollup with `brief_line`) plus a `suppressed` set (children + trackers, excluded from flat doing lists). Used by `pm_context` (MCP, `trackers` section) and `pm context` (CLI). The generated rollup replaces hand-maintained board tables in the parent body. Subtask lifecycle: `todo → doing → merged → done` (add `merged` to the project's `statuses`).
- **Brief field**: `brief` in frontmatter stores short session context ("where we left off"). Overwrites on each update (not append). Preserved on all status transitions (including done/archived - useful for summaries/reverts). Returned by `pm_context` for doing tasks.
- **AC field**: `ac` in frontmatter stores acceptance criteria. Overwrites on each update (same as brief). Preserved on all status transitions. Returned in `pm_context` and `pm_list_tasks` summaries. When executing a task, constrain work to AC - don't add unrequested features or deviate from stated criteria.
- **Spec / Log body zones**: the body splits into two zones with opposite write rules, solving append-only-rot on long-lived tasks. The **Spec** zone (delimited by `<!-- spec:start -->`/`<!-- spec:end -->` markers, written via the `spec` param) is the current-truth document - rewritten wholesale in place via `storage.ApplySpec(body, spec)` (`internal/storage/spec.go`). Everything outside the markers is the append-only **Log** (written via `body_append`). `storage.ExtractSpec(body)` returns the Spec content. The TUI detail view renders the markers as visible `## Spec (current truth)` / `## Log (history, append-only)` headers (`bodyToDisplayMarkdown` in `view.go`). Markers default to the top of the body (Spec first, Log below); if absent when `spec` is first set, a block is prepended and the existing body becomes the Log. Canonical flow: rewrite Spec + append a one-line pointer to the Log.
- **Project config**: `~/.claude/pm/<slug>/project.yaml` — name, path, repo, stack, notes, links, tags, statuses
- **Statuses are per-project** — read via `Store.GetProjectStatuses(slug)`. Default: `[todo, doing, waiting, done]`. Override in `project.yaml`. "ALL" view merges all projects' statuses.
- **Archive is system-level** — `StatusArchived` is NOT a project status. Never add to `DefaultStatuses` or `project.yaml`. `filteredTasks()` excludes archived. `Ctrl+a` toggles archive view.
- **TUI has 4 views**: `viewBoard`, `viewDetail`, `viewArchive`, `viewProjectInfo`. `previousView` controls where detail/escape returns to.

## TUI patterns

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
- **`pm run-epic [project] <tracker>`** (`internal/cmd/run_epic.go`) — the MANAGER. Creates integration branch `epic/<tracker>`, drives ready subs sequentially (by `Order`), each on `feat/<slug>` branched off the integration branch; merges back `--no-ff` on verify-green → `done_status`. Blocked/failed/conflict → park on `waiting` + continue. Re-entrant (skips merged/done). Ends with ONE draft `epic→main` PR. Never merges to main, never closes the parent.
- **Profile** (`internal/storage/executor.go`): an `executor` block in `project.yaml` binds each phase (`implement/test/review/verify/pr`) to `skill:` | `cmd:` | empty (built-in generic) | `false` (skip). No block = all generics, `worktree:false`. `pm executor init <project>` (`internal/cmd/executor_init.go`) auto-detects skills+stack and drafts it.
- **Autonomy envelope** (hard rules, in `buildWorkerSystemPrompt`): stay within AC (no scope creep), never merge to main / force-push / hard-reset, commit on the given branch only. Permissions: `acceptEdits` + curated bash allowlist + disallow force-push/merge/hard-reset (`--yolo` bypasses).
- **Worker = `claude -p`**, NO git worktree by default (RN/JS worktrees lack node_modules — opt-in `worktree:true` only for shared-cache toolchains). Headless workers CANNOT use interactive-auth MCP (e.g. figma) → design-heavy tasks need a manual visual check.
