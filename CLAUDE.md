# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
go install -ldflags "-X github.com/mbalazy/pm/internal/version.Version=X.Y.Z" ./cmd/pm/
```

IMPORTANT: Always set version via ldflags. Bump version on each release. Current: **0.6.0**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN). Always use `go install`, not `go build`.

No tests yet. Always verify with `go vet ./...` after changes.

## Key patterns

- **Frontmatter tasks**: `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, tags) + markdown body
- **Project config**: `~/.claude/pm/<slug>/project.yaml` — name, path, repo, stack, notes, links, tags, statuses
- **Statuses are per-project** — read via `Store.GetProjectStatuses(slug)`. Default: `[todo, doing, done]`. Override in `project.yaml`. "ALL" view merges all projects' statuses.
- **Archive is system-level** — `StatusArchived` is NOT a project status. Never add to `DefaultStatuses` or `project.yaml`. `filteredTasks()` excludes archived. `Ctrl+a` toggles archive view.
- **TUI has 4 views**: `viewBoard`, `viewDetail`, `viewArchive`, `viewProjectInfo`. `previousView` controls where detail/escape returns to.

## TUI patterns

- **Confirmation pattern**: `d` (done), `x` (delete), and `q` (quit) require double-press. `d`/`x` validated by task ID — if cursor moves between presses, re-confirms on new task.
- **Overlay menus**: `yankMenu` (`Y`) and `linksMenu` (`L`) intercept input before all other handlers. Single link opens directly via `open`, multiple shows picker.
- **Toast**: `copyToClipboard()` uses `pbcopy` + 2s toast overlay. Auto-clears on tick refresh.
- **`reload()`**: canonical way to refresh state — calls `loadTasks()` + `loadProjectCounts()` + `fixCursors()`. Don't call these separately.
- **Tab counts**: `projectCounts map[string]int` shows non-archived task count per project in tab labels.
- **hjkl navigation**: `h`=left, `j`=down, `k`=up, `l`=right. Links menu is `L` (uppercase).

## Gotchas

- `storage.DefaultStatuses` is the single source of truth for defaults — don't hardcode `[todo, doing, done]` elsewhere
- Board cursors are `[]int` (dynamic length matching statuses) — not a fixed array
- `ParseStatus` returns `TaskStatus`, not `(TaskStatus, error)` — it lowercases any string
- Task files live next to `project.yaml` in the same dir (not in a subdirectory)
- Links is `map[string]string` (freeform key=url), not a typed struct
- Version is set via ldflags at build time (`internal/version/version.go`) — defaults to "dev" without ldflags
- `l` (lowercase) is Right arrow navigation, `L` (uppercase) is links — don't bind `l` to anything else
- `store.DeleteTask()` removes the file from disk — but undo (`z`) can restore via `snapshotTask()` + `WriteTask()`
- Archived tasks are invisible on board but still on disk — restore with `u` in archive view sets first project status
- **Undo** (`z`): single-step undo for move/done/archive/delete. `snapshotTask()` deep-copies before action, `WriteTask()` restores. `lastUndo` is nil after undo (no redo).
- **Overlay menus** close with `esc`, `q`, or `h` (left)

## MCP server (`pm mcp`)

Stdio MCP server for Claude Code. Registered as user-scope MCP: `claude mcp add --transport stdio --scope user pm -- ~/.local/share/go/bin/pm mcp`.

- **Package**: `internal/mcpserver/` — `server.go` (setup + Run), `tools.go` (7 tools), `resources.go` (3 resources)
- **Subcommand**: `internal/cmd/mcp.go` — `pm mcp` cobra command
- **SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` — typed `AddTool[In, Out]` for auto schema generation
- **Tools**: `pm_context`, `pm_list_tasks`, `pm_get_task`, `pm_list_projects`, `pm_add_task`, `pm_update_task`, `pm_move_task`
- **Resources**: `pm://projects`, `pm://tasks/{project}/{status}`, `pm://project/{slug}`
- **CWD auto-detection**: `pm_context` with `cwd` param matches against project.yaml `path` fields
- **Invariants**: links merge (never remove), body appends (never replace), tags replace if provided
