# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
go install -buildvcs=false -ldflags "-X github.com/mbalazy/pm/internal/version.Version=X.Y.Z" ./cmd/pm/
```

IMPORTANT: Always set version via ldflags. Bump version on each release. Current: **0.7.0**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN). Always use `go install`, not `go build`.

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

- **Frontmatter tasks**: `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, tags, brief) + markdown body
- **Brief field**: `brief` in frontmatter stores short session context ("where we left off"). Overwrites on each update (not append). Cleared when task moves to done/archived. Returned by `pm_context` for doing tasks.
- **Project config**: `~/.claude/pm/<slug>/project.yaml` — name, path, repo, stack, notes, links, tags, statuses
- **Statuses are per-project** — read via `Store.GetProjectStatuses(slug)`. Default: `[todo, doing, waiting, done]`. Override in `project.yaml`. "ALL" view merges all projects' statuses.
- **Archive is system-level** — `StatusArchived` is NOT a project status. Never add to `DefaultStatuses` or `project.yaml`. `filteredTasks()` excludes archived. `Ctrl+a` toggles archive view.
- **TUI has 4 views**: `viewBoard`, `viewDetail`, `viewArchive`, `viewProjectInfo`. `previousView` controls where detail/escape returns to.

## TUI patterns

- **`reload()`**: canonical way to refresh state - calls `loadTasks()` + `loadProjectCounts()` + `fixCursors()`. Don't call these separately.
- **Tab counts**: `projectCounts map[string]int` shows non-archived task count per project in tab labels.
- See `.claude/skills/tui-patterns/` for overlay menus, confirmation pattern, toast, navigation details.

## Gotchas

- `storage.DefaultStatuses` is the single source of truth for defaults - don't hardcode `[todo, doing, done]` elsewhere
- `ParseStatus` returns `TaskStatus`, not `(TaskStatus, error)` - it lowercases any string
- Task files live next to `project.yaml` in the same dir (not in a subdirectory)
- Links is `map[string]string` (freeform key=url), not a typed struct
- Version is set via ldflags at build time (`internal/version/version.go`) - defaults to "dev" without ldflags

## Workflow

- Branch: `main` only (no feature branches for solo project)
- Commit messages: imperative, concise, prefix with feat/fix/refactor
- Always bump version in build command when releasing

## MCP server (`pm mcp`)

Stdio MCP server for Claude Code. Registered as user-scope MCP: `claude mcp add --transport stdio --scope user pm -- ~/.local/share/go/bin/pm mcp`.

- **Package**: `internal/mcpserver/` — `server.go` (setup + Run), `tools.go` (7 tools), `resources.go` (3 resources)
- **Subcommand**: `internal/cmd/mcp.go` — `pm mcp` cobra command
- **SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` — typed `AddTool[In, Out]` for auto schema generation
- **Tools**: `pm_context`, `pm_list_tasks`, `pm_get_task`, `pm_list_projects`, `pm_add_task`, `pm_update_task`, `pm_move_task`
- **Resources**: `pm://projects`, `pm://tasks/{project}/{status}`, `pm://project/{slug}`
- **CWD auto-detection**: `pm_context` with `cwd` param matches against project.yaml `path` fields
- **Invariants**: links merge (never remove), body appends (never replace), brief overwrites, tags replace if provided
