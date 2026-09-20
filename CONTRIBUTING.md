# Contributing

pm is a personal tool built for one workflow, so it is not looking for
features. Bug reports are welcome; open an issue before a pull request.

If you do send a patch:

- `make check` (gofmt, vet, staticcheck, tests) has to pass. `git config
  core.hooksPath githooks` points git at the versioned pre-commit hook, which
  runs exactly that. Never bypass it with `--no-verify`.
- staticcheck is not vendored:
  `go install honnef.co/go/tools/cmd/staticcheck@v0.6.1`, the version CI
  pins. Put `$(go env GOPATH)/bin` on your `PATH`.
- Changes under `web/` also need `make web-check` (oxlint, prettier, tsc,
  vitest), and `make web-install` once to fetch the dependencies.
- The test suite needs `git` and `python3` on `PATH` (the change feed's
  Slack tests drive a fake MCP server written in Python). The agent paths
  run against a fake `claude` binary, so they cost no tokens.
- Add the test with the change. Table-driven, `t.TempDir()` for anything
  touching the filesystem, same package as the code under test.
- Commit messages are imperative and prefixed `feat:`, `fix:`, `refactor:`,
  `docs:` or `chore:`.

`CLAUDE.md` in the repo root is the contributor guide proper: one rule per
invariant, each stated with the reason it exists.
