# Changelog

One line per release, newest first. The version lives in the Makefile
(`VERSION`) and is stamped into the binary through ldflags, so every entry
below is a commit that changed that line. `git log -L 1,1:Makefile` prints
that line's history, with one catch: it does not diff merge commits, and
0.61.0 came in on one, so that output never shows it being set - `git show
5e3dec2:Makefile` does.

There are no git tags, and versions promise each other nothing. The only
promise is that your task files stay readable, because they are markdown.

## 0.64.0 - 2026-09-20

Prepared for a public repository: the module path is now
`github.com/mbalazy/pm-cli`, a binary built without ldflags reports its
version from Go's build info instead of the word `dev`, the README was
rewritten for a stranger, and the long-form documentation moved under
[docs/](docs/README.md).

## 0.63.0 - 2026-09-16

The embedded agent guide slimmed to rules only, 22.0 KB down to 12.7 KB: a
guide that is pasted into every session pays for its own length.

## 0.62.0 - 2026-09-16

Timeline state lines carry their provenance - `[verified YYYY-MM-DD by
<command>]` or `[assumed]` - and a verified line older than a week is
reported as due for a re-check.

## 0.61.0 - 2026-09-16

Start an unattended solo session from the cockpit, as a `claude --bg`
background session.

## 0.60.0 - 2026-09-15

`pm timeline`: what happened *to* a project - events, decisions and dated
state snapshots - next to what happened *in* a task.

## 0.59.0 - 2026-09-06

Recorded what a false green taught: a server that answers is not an app that
mounted. The web runtime rig restarts Vite for every run and needs a browser
to see the shell render before it reports itself up.

## 0.58.1 - 2026-09-06

A pull request merged into the base branch closes its task: the agent moves
it to done itself, because a merge is evidence rather than a judgement call.

## 0.58.0 - 2026-09-05

The cockpit: `pm serve` with a JSON API, a change feed, an SSE event stream
and a React front end.

## 0.57.0 - 2026-09-05

A runtime phase in the executor, with a per-project rig hook.

## 0.56.0 - 2026-09-05

`executor.model` and `executor.effort` in the project profile, an `--effort`
flag, and `--tools` on the worker's argv.

## 0.55.0 - 2026-09-02

Per-subtask token accounting from the result envelope's `usage`: tokens, not
dollars, are what a subscription window meters. Workers can run under a slim
`executor.worker_claude_config_dir`.

## 0.54.3 and earlier

69 more releases, back to 0.7.4 on 2026-03-05 - the oldest version the
Makefile's `VERSION` line records. See `git log -L 1,1:Makefile`.
