# Docs

One file per surface, each readable cold. The README is the two-minute landing page; this is the index.

**Using pm**

- [cli.md](cli.md) - every command and flag of the `pm` binary, which ones speak `--json`, the environment variables it reads and sets, the binaries it shells out to.
- [data-model.md](data-model.md) - what is on disk: the data directory, a task file field by field with its write rules, statuses, the Spec/Log zones, `project.yaml` and its `executor:` block, the global `config.yaml`, the attention queue's sections, locking.
- [mcp.md](mcp.md) - the 14 tools of `pm mcp` as a reference: parameters, tri-state rules, output shapes, budgets, the traps.
- [board.md](board.md) - the TUI: views, every key, overlays, the launch menu and what each launch actually runs.
- [cockpit.md](cockpit.md) - `pm serve` and the web cockpit: first how to use it (screens, keys, the API routes, what the API writes, the security stance), then the design record screen by screen.
- [journals-and-timeline.md](journals-and-timeline.md) - the two per-project records: a journal of one troublesome subsystem, and the timeline of what happened to the project with its provenance-marked state snapshots.
- [solo-and-batch.md](solo-and-batch.md) - working while nobody watches: a solo shift (one Claude Code background session on a queue, launched from the cockpit, its state and report read back from `.shift/`) and a batch (`pm run-epic` in independent mode, then `pm finish`), each as a recipe; the two skills the runs follow live in [mbalazy/claude-skills](https://github.com/mbalazy/claude-skills), not here.
- [executor.md](executor.md) - the executor: first the manual for `pm work`, `pm run-epic` and `pm finish`, then their internals - worktree slots, the review policy and the hook that enforces it, run state and journal, kill and crash forensics, acceptance claims, the verification baseline.

**Written for the agent** (embedded in the binary, printed by `pm docs`)

- [agent-guide.md](agent-guide.md) - the usage contract an agent works to: when to record what, the brief format, the Spec/Log write rules, blockers, journals, timelines, and who closes a task - a merged pull request does it, everything else waits for the user. Printed by `pm docs claude`, which is how it reaches your own `CLAUDE.md`.
- [task-authoring.md](task-authoring.md) - how to write a task a session can pick up cold, printed by `pm docs authoring`.

**Working on pm**

- [development.md](development.md) - build, test, the web toolchain, CI, releasing, the deploy target, and the one rule about names in a public repo.
- [design-log.md](design-log.md) - the rationale, the bug stories and the measurements behind the rules in `CLAUDE.md`: what was tried, what it cost, why the current shape won. A rule ending `(history: docs/design-log.md)` has its story here.

[`CLAUDE.md`](../CLAUDE.md) at the repo root is the agent's working memory and the contributor guide - one rule per invariant, each with its why. The two agent files above are what the binary prints for people *using* pm; CLAUDE.md is for whoever edits it.
