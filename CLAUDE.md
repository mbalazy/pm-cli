# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
go install -buildvcs=false -ldflags "-X github.com/mbalazy/pm/internal/version.Version=X.Y.Z" ./cmd/pm/
```

IMPORTANT: Always set version via ldflags. Bump version on each release. Current: **0.7.3**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN). Always use `go install`, not `go build`.

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
- Invariant tests (links merge, body append, brief lifecycle) live in `tools_test.go`

## Verification

- After code changes: `go vet ./...` && `go test ./internal/... -count=1`
- After TUI changes: rebuild with `go install` (see Build above) + run `pm board` and test interactively
- After MCP changes: rebuild + test with `pm_context` / `pm_list_tasks` in Claude Code
- After storage changes: verify task files in `~/.claude/pm/` have correct frontmatter

## Key patterns

- **Frontmatter tasks**: `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, tags, brief, order) + markdown body
- **Brief field**: `brief` in frontmatter stores short session context ("where we left off"). Overwrites on each update (not append). Cleared when task moves to done/archived. Returned by `pm_context` for doing tasks.
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

## Workflow

- Branch: `main` only (no feature branches for solo project)
- Commit messages: imperative, concise, prefix with feat/fix/refactor
- Always bump version in build command when releasing

## MCP server (`pm mcp`)

Stdio MCP server for Claude Code. Registered as user-scope MCP: `claude mcp add --transport stdio --scope user pm -- ~/.local/share/go/bin/pm mcp`.

- **Package**: `internal/mcpserver/` — `server.go` (setup + Run), `tools.go` (8 tools), `resources.go` (3 resources)
- **Subcommand**: `internal/cmd/mcp.go` — `pm mcp` cobra command
- **SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` — typed `AddTool[In, Out]` for auto schema generation
- **Tools**: `pm_context`, `pm_list_tasks`, `pm_get_task`, `pm_list_projects`, `pm_add_task`, `pm_update_task`, `pm_update_project`, `pm_create_project`, `pm_move_task`, `pm_delete_task`
- **Resources**: `pm://projects`, `pm://tasks/{project}/{status}`, `pm://project/{slug}`
- **CWD auto-detection**: `pm_context` with `cwd` param matches against project.yaml `path` fields
- **Invariants**: links merge (never remove), body appends (never replace), brief overwrites, tags replace if provided
