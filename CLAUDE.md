# pm-cli

Local task tracker. Data: `~/.claude/pm/<project-slug>/`, binary: `pm`.

## Build

```
go install -ldflags "-X github.com/mbalazy/pm/internal/version.Version=X.Y.Z" ./cmd/pm/
```

IMPORTANT: Always set version via ldflags. Bump version on each release. Current: **0.4.0**. Binary goes to `~/.local/share/go/bin/pm` (GOBIN). Always use `go install`, not `go build`.

No tests yet. Always verify with `go vet ./...` after changes.

## Key patterns

- **Frontmatter tasks**: each task is a `.md` file with YAML frontmatter (id, title, status, created, updated, links, branch, tags) + markdown body
- **Project config**: `~/.claude/pm/<slug>/project.yaml` — name, path, repo, links, tags, statuses
- **TUI**: Bubble Tea board with dynamic columns based on project statuses. Cobra CLI.
- **ParseStatus accepts any string** — validation is caller's responsibility via `ValidateStatus(status, allowed)`
- **No global valid statuses** — statuses are per-project, read via `Project.GetStatuses()` or `Store.GetProjectStatuses(slug)`

## Custom statuses

Default: `[todo, doing, done]`. Override per project in `project.yaml`:

```yaml
statuses: [backlog, todo, doing, review, done]
```

- Missing/empty `statuses` → fallback to default
- Board columns, move (m/M), and status counts all use dynamic statuses
- "ALL" view merges statuses from all projects (defaults first, then unique extras)
- Existing tasks are unaffected — no migration needed

## Gotchas

- `storage.DefaultStatuses` is the single source of truth for defaults — don't hardcode `[todo, doing, done]` elsewhere
- Board cursors are `[]int` (dynamic length matching statuses) — not a fixed array
- `ParseStatus` returns `TaskStatus`, not `(TaskStatus, error)` — it lowercases any string
- Task files live next to `project.yaml` in the same dir (not in a subdirectory)
- Links is `map[string]string` (freeform key=url), not a typed struct
- Version is set via ldflags at build time (`internal/version/version.go`) — defaults to "dev" without ldflags
