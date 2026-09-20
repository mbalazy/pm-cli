# Docs

- [agent-guide.md](agent-guide.md) - the usage contract an agent works to: when to record what, the brief format, the Spec/Log write rules, blockers, journals, timelines, and the rule that only a human closes a task. It is embedded in the binary and printed by `pm docs claude`, which is how it reaches your own `CLAUDE.md`.
- [task-authoring.md](task-authoring.md) - how to write a task a session can pick up cold, printed by `pm docs authoring`. Also embedded.
- [executor.md](executor.md) - the executor: first the manual for `pm work`, `pm run-epic` and `pm finish`, then their internals - worktree slots, the review policy and the hook that enforces it, run state and journal, kill and crash forensics, acceptance claims, the verification baseline.
- [cockpit.md](cockpit.md) - the design record of `pm serve` and the web cockpit, screen by screen: what each one shows, where its data comes from, which decisions are load-bearing.
- [design-log.md](design-log.md) - the rationale, the bug stories and the measurements, moved verbatim out of `CLAUDE.md` and filed under the section each one came from: what was tried, what it cost, why the current shape won. A rule ending `(history: docs/design-log.md)` has its story here.

[`CLAUDE.md`](../CLAUDE.md) at the repo root is the agent's working memory and the contributor guide - one rule per invariant, each with its why. The first two files above are what the binary prints for people *using* pm; CLAUDE.md is for whoever edits it.
