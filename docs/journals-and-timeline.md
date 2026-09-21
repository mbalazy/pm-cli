# Journals and the timeline

Two per-project records that are neither tasks nor docs: a **journal** is how one repeatedly-troublesome subsystem actually behaves, entry by entry; the **timeline** is what happened *to* the project, plus dated snapshots of where it stands. Both are append-only JSONL under the project's data directory, both have a CLI and a pair of MCP tools, and both exist because a rule file cannot answer "fifth time this month" or "where did we stand on the 27th". This page is for whoever writes to them - a person at the shell or an agent over MCP - and says what each one is for, what an entry holds, and how the reads work. Storage: `internal/storage/journal.go`, `internal/storage/timeline.go`, `internal/storage/timeline_verify.go`; CLI: `internal/cmd/journal.go`, `internal/cmd/timeline.go`; MCP: `internal/service/journal.go`, `internal/service/timeline.go`.

## Journals

### What one is for

A journal tracks ONE chosen subsystem of a project that keeps biting - a simulator rig, a flaky sandbox, a deploy pipeline. Each entry records what it looked like before the cause was known, the wrong conclusion the symptom produced, the real cause, the minutes lost and the fix it argues for. The point is recurrence: a memory file or a doc holds a **rule** and gets rewritten when the rule turns out wrong; a journal entry is an **event**, dated and never rewritten, so the pile can be counted, clustered by tag and month, and turned into the argument for a flow or tool change. An entry names the rule it produced in `fix`; an entry with no fix is OPEN, and the open set is the backlog - derived, never maintained by hand.

Which subsystems a project journals is declared in `project.yaml`; nothing in pm infers a subject:

```yaml
journals:
    - name: sim-rig
      subject: iOS simulator rig (which sim, which Metro, which build)
```

The `name` is a file name (validated like a project slug: lowercase, digits, `.`, `_`, `-`), and an undeclared name is an error in both the CLI and MCP - never an implicit create, because a typo would silently open a second file and split one history in half. Entries live at `<project data dir>/.journal/<name>.jsonl`, one JSON object per line.

### An entry

| field | meaning |
|---|---|
| `id` | derived from `ts` and `symptom` (`YYYYMMDD-<4 hex>`), so a line written before ids existed still has one and can be closed later |
| `ts` | RFC3339, stamped on append; `--date` / `date` back-dates it when seeding history |
| `symptom` | required: what it looked like before the cause was known - the thing a future reader recognises |
| `false_conclusion` | the wrong belief the symptom produced |
| `cause` | what was actually going on |
| `cost_min` | minutes lost |
| `fix` | the flow or tool change and where it landed; **empty = OPEN**, and "n/a" is not empty |
| `tags` | group incidents that share a cause |
| `session` | the Claude session id, when written from one |
| `resolves` | ids of earlier entries this one closes |

Closing an entry is a new event, never an edit: the fix arrives as a later entry pointing back through `resolves`. An id in `resolves` that does not exist in that journal is rejected before anything is written. An entry is still open when it has no `fix` of its own AND no later entry resolves it (`StillOpen` in `internal/storage/journal.go`).

Reads come back newest first; an entry whose `ts` does not parse sorts last, in both directions. The stats rollup counts total, open, closed-by-a-later-entry, cost, tags by count, months in order, and lists the open entries newest first. The cost average divides by the entries that recorded a cost, not by the total - dividing by the total would make a subsystem look cheaper the more often someone skipped the field.

### CLI

`pm journal` takes `-p/--project` (default: the project detected from the current directory) and is the same as `pm journal list`.

| command | flags | what it does |
|---|---|---|
| `pm journal list` | | the declared journals with entry and open counts |
| `pm journal show <name>` | `--limit` (20, `0` = all), `--open` | entries newest first: date, id, symptom, wrong conclusion, cause, fix or `OPEN` or "closed by a later entry", closes, minutes, tags |
| `pm journal add <name>` | `--symptom` (required), `--false`, `--cause`, `--cost`, `--fix`, `--tag` (repeatable), `--session`, `--resolves` (repeatable), `--date YYYY-MM-DD` | appends one entry; prints whether it is open or carries a fix |
| `pm journal stats <name>` | | recurrence, cost and the open backlog |

### MCP

- `pm_journal_add` - `project`, `name`, `symptom` required; `false_conclusion`, `cause`, `cost_min`, `fix`, `tags`, `session`, `date`, `resolves` optional. Answers the entry plus `total`, `open` and a `note` saying what recurrence this entry produced ("entry 5 in this journal; 2 open; 3 entries now share tag ...").
- `pm_journal_list` - `project` required. Without `name`: the declared journals with counts (`declared`). With `name`: that journal's entries newest first (`limit` default 20, cap 200; `open: true` for the backlog only) together with the rollup: `total`, `shown`, `open`, `closed_later`, `cost_min`, `tags`, `months`, `subject`.

`pm_context` and `pm context` carry **counts only** - `<name> N entries, M open` and a pointer to `pm journal show` - never the entries. A journal grows without bound and its readers are the sessions about to touch that subsystem, not every session that starts. The rule for an agent is to read the journal before touching the subsystem it covers: it holds the failures that already cost time, including the ones that produced confident wrong answers.

## Timeline

### What it is for

A task's Log says what happened *in* the task. The timeline says what happened *to* the project: a direction change, a client decision, a milestone, a new person, a document arriving - and, at intervals, a snapshot of where the project stands. Commits, pull requests and the cockpit's change feed are machine events and never go here. There is one timeline per project, nothing to declare.

Three kinds, and nothing else:

| kind | size rule | what it is |
|---|---|---|
| `event` | one to three sentences | something happened |
| `decision` | one sentence plus `refs` to the full record (a task, a doc) | why the picture changed |
| `state` | 10-20 lines that fit on a screen: where we stand, what blocks, what is next, open questions, links | a dated snapshot; longer material stays in a document the state refers to |

A state is an entry, not a field. Entries are never edited: a changed picture is a NEW state, and older states stay, so "where did we stand on 27.08" stays answerable. That is the difference from a task brief, which overwrites because a task ends; a project does not.

### An entry

| field | meaning |
|---|---|
| `id` | derived from `ts`, `kind` and `text` |
| `ts` | RFC3339; an entry dated in the future is refused |
| `kind` | `event`, `decision` or `state` |
| `text` | the entry |
| `refs` | task ids, paths or URLs, stored verbatim |
| `session` | the Claude session id, when written from one |

Files are `<project data dir>/.timeline/<YYYY-MM>.jsonl`, the month taken from the entry's own timestamp. Reads skip a line that does not parse, has no text or an unknown kind, and impose no line-length cap, so a long state survives. Back-dating with `--date` puts the entry at midnight of that day, EXCEPT today, which takes the current time: a state written at 10:00 with today's date must not sort before the 09:00 state written without the flag, or the default read would show the older one.

### The default read

Never read a state alone - it goes out of date with the first entry written after it. The default read is the latest **dated** state in full plus every entry after it, oldest first. It carries `stale: true` once 10 entries or 7 days have passed since that state (`TimelineStaleEntries`, `TimelineStaleDays`), which is the cue to write a new one. With no state yet it returns the newest 10 entries and says so in `note`.

### Provenance markers

A state is prose someone wrote, and after a context compaction the next reader cannot tell a fact that was seen from one that was inferred. So every state line ends with its provenance:

- `[verified YYYY-MM-DD by <command or doc>]` - checked in THIS session; the day the command was run, and the command or document that showed it.
- `[assumed]` - not checked.

Markers are parsed on read only (`ParseStateLines` and `VerifyState` in `internal/storage/timeline_verify.go`); the stored text is untouched, so an older state reads as all-unmarked. The read's `verification` block counts `verified`, `recheck`, `assumed` and `unmarked` lines and lists in `recheck_lines` the verified lines 7 or more days old (`TimelineRecheckDays`): re-establish those with a tool call before relying on them. A line with no marker is never counted as verified; a `[verified]` marker whose day cannot be read is due at once. `event` and `decision` entries carry `refs` instead and take no marker.

The write side refuses a state whose `[verified ...]` has no `YYYY-MM-DD` or a future day - stored, it would read as verified in the raw text while the parser counted it unmarked. Never copy a `[verified]` marker forward from an older state without re-running its source.

### CLI

`pm timeline` takes `-p/--project`, or the project as its positional argument (positional beats the flag, then the current directory).

| command | flags | what it does |
|---|---|---|
| `pm timeline [project]` | `--json` | the default read; under the state header a `verification: ...` line, and on a marked state a gutter: ` ! ` a verified line due for re-check with its age, ` ~ ` an assumed line; a `stale:` line when due |
| `pm timeline add` | `--kind` (required), `--text` (required; `-` reads stdin, the way to pass a multi-line state), `--ref` (repeatable), `--session` (detected inside Claude Code), `--date YYYY-MM-DD` | appends one entry; for a state prints the verification summary; prints the stale note when due |
| `pm timeline list` | `--since YYYY-MM-DD`, `--kind`, `--json` | entries newest first across every month, `{entries, matched, total}` as JSON |

### MCP

- `pm_timeline_add` - `project`, `kind`, `text` required; `refs`, `session` optional. Kind, text and the state markers are checked before the append. Answers the entry, `stale`, `entries_since`, `days_since` and a note.
- `pm_timeline_list` - `project` required. With no `since` / `kind` / `limit` it is the default read: `state`, `since` (the entries after it), `stale`, `entries_since`, `days_since`, `total`, `verification`, `note`. With any of them it is a filtered list newest first, `limit` default 20 and cap 200, as `{entries, total, shown, note}` - two different shapes, so a client branches on which it asked for. Reach for the filtered form when the state does not say WHY (a decision), for what happened between two dates, or when a task contradicts the state.

### Where else it shows

- Project-scoped `pm_context` carries a `timeline` block: the state whole, the newest 20 entries after it (`since_omitted` says how many more), `verification`, `stale`, the note. Absent when the project has no entry.
- The cross-project `pm_context` rollup carries only `timeline_state`: `YYYY-MM-DD: <first line>`, with ` (stale)` and ` (N to recheck)` appended when they apply.
- The cockpit's group page shows the same default read under "Where we left off", with the verification line; `/api/timeline/{project}` is its endpoint.

The agent rule that ties the two records together: write to the timeline at the same checkpoint where a task brief is saved, when something happened at project level - most sessions write nothing here - and read the latest state PLUS every entry after it, never the state alone.
