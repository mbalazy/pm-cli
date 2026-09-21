# Development

How to build, test and ship pm from a clone: the make targets, the git hooks, the test conventions, the web toolchain, the runner deploy and CI. This is for whoever edits the code; `CLAUDE.md` at the repo root is the fuller contributor guide with one rule per invariant and the reason behind each.

## Build and check

```sh
make check        # gofmt-check + vet + staticcheck + go test
make test-race    # the race detector, also run by CI
make install      # build and install, version through ldflags
make web-install  # npm ci in web/ (once)
make web-check    # lint, tsc and vitest for the web
make install-full # the front end plus the binary
```

Git hooks live in `githooks/` (`git config core.hooksPath githooks`) and pre-commit runs `make check`, the same target as CI. `make check` and `make install` are node-free on purpose, which is why the cockpit's front end has its own target. The tests need `git` and `python3` on `PATH`, and the agent paths run against a fake `claude` binary - a PATH-prepended script emitting a canned result envelope - so the real subprocess, parsing, journal and run-state plumbing is covered at zero token cost.

Always install with `make install`, never a raw `go install`: the Makefile stamps `VERSION` into `internal/version` through ldflags, and a binary without it reports the module version or `dev`. The binary lands in `go env GOBIN`, or `$(go env GOPATH)/bin` when that is empty.

`staticcheck` is resolved from `PATH`, then `go env GOBIN`, then `GOPATH/bin`, and fails with an install hint when missing: `go install honnef.co/go/tools/cmd/staticcheck@v0.6.1`. That is the version `.github/workflows/ci.yml` installs; bump both together, or a check that passes locally fails in CI. Fix hook failures; never bypass them with `-n`.

## Tests

```sh
go test ./internal/... -v                          # all tests
go test ./internal/storage/ -v                     # one package
go test ./internal/storage/ -run TestSlugify -v    # one test
```

- Go stdlib `testing`, table-driven, `t.Run("case", ...)` subtests, `t.TempDir()` for anything that touches disk.
- Tests live in the same package as the code so they reach unexported functions. `setupTestStore(t)` in `internal/storage/store_test.go` builds a store with a project and tasks.
- The write-rule invariants (links merge, Log appends, Spec rewrites, brief lifecycle) live in `internal/mcpserver/tools_test.go`; `ApplySpec` and `ExtractSpec` have unit tests in `internal/storage/spec_test.go`.
- Executor paths run through the fake `claude` binary (`fakeClaude` in `internal/cmd/work_exec_test.go`), covering the verified, blocked, crash and garbage-output paths of a worker. The envelope has two shapes, an object or a message array whose last element is the object.
- MCP handlers are tested end to end through the SDK's in-memory transport (`startMCP` in `internal/mcpserver/e2e_test.go`) so the real registered closures run; the test body never re-simulates handler logic.
- Lock and race invariants live in `internal/storage/lock_test.go` and the two-acquirer hammer in `internal/storage/worktree_test.go`. Run suspects under `-race`; `make test-race` runs the whole suite that way before anything touching locking is pushed.
- HTTP routes are tested in `internal/server/server_test.go` with `httptest` over the same temp-store fixture, including the SSE feed and the remote-runs hook through a fake, never ssh.

## The web front end

The cockpit SPA lives in `web/`: Vite, React, TypeScript in `strict` mode, Tailwind, TanStack Router and Query, oxlint plus prettier, vitest with testing-library. `web/package.json` scripts:

| script | runs |
|---|---|
| `dev` | `vite` with `/api` proxied to `127.0.0.1:7070` |
| `build` | `tsc -b && vite build` into `internal/server/dist` |
| `lint` | `oxlint && prettier --check .` |
| `format` | `prettier --write .` |
| `typecheck` | `tsc -b` |
| `test` | `vitest` |
| `preview` | `vite preview` |

`make web-install` is `npm ci` (it needs the versioned `web/package-lock.json`), `make web-check` is lint, typecheck and `vitest --run`, `make web` builds the bundle and touches `internal/server/dist/.gitkeep` back, because Vite empties the directory and would otherwise show the file as deleted. `internal/server` embeds `dist/` with `//go:embed all:dist`; only `.gitkeep` is versioned, so `go build` works with no bundle and `pm serve` shows a placeholder page saying to run `make web`. `make install-full` is `web` then `install`, the one command that ships the cockpit.

The layer boundary that keeps the UI replaceable: `web/src/api/` (hand-written mirrors of the Go DTOs, the fetch wrapper, the query hooks) and `web/src/lib/` (pure functions, each with a test) are permanent; `web/src/routes/` and `web/src/components/` are the replaceable part. A DTO field added in Go is added to `web/src/api/types.ts` by hand; there is no generator.

## Deploying to a runner

`make deploy-vps` cross-compiles a Linux binary (`make build-pm-linux`) and installs it on a remote box that runs executor batches. `VPS_HOST` (default `runner`, an ssh host alias) and `VPS_BIN` (default `/home/runner/go/bin/pm`) are overridable. Two guards, both skipped by `FORCE=1`: a dirty working tree is refused, because the deployed binary could not be traced to a commit; and a live `pm work` or `pm run-epic` on the host is refused, because the binary would change under a running batch. The recipe ends by printing both machines' `pm --version`. Nothing syncs the runner's binary otherwise; after a release the laptop and the runner diverge silently until this target runs.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every pull request, in two jobs:

- `check`: `make fmt-check`, `make vet`, `make staticcheck` (v0.6.1 installed first), `make test-race`. Same targets as the local hook, plus the race detector, which is deliberately not part of `make check` so the commit-time gate stays fast.
- `web`: `make web-install`, `make web-check`, `make web`.

## Releasing

The version is the `VERSION` line in the `Makefile` and nothing else; bump it in the release commit and add one line to `CHANGELOG.md`, newest first. There are no git tags, and versions promise each other nothing beyond task files staying readable markdown. `git log -L 1,1:Makefile` prints the version line's history.

## Workflow

Substantive work (features, fixes, refactors, review-fix batches) goes on a feature branch and merges through a pull request; small standalone tweaks may land directly on `main`. Commit messages are imperative and short, prefixed `feat`, `fix`, `refactor`, `docs` or `chore`.

## Public repo: no real names

This repository is public and its history was rewritten once to remove names; that is not available again, so nothing that must not be public goes in at all. No real client, employer, colleague, host, IP, ticket board, private alias or path under a real person's home directory, anywhere: code, comments, tests, fixtures, docs, commit messages and branch names. Fixtures reuse the names already in the tree (`acme`, `orbit`, `vega`, `atlas`, `runner`, `/Users/alice`, `example.com`, `ACME-253`). `CLAUDE.md` holds the table and the grep to run over a staged diff before committing prose or fixtures.
