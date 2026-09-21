# The board (`pm board`)

The terminal UI: a kanban over the task files, one tab per project, vim keys, and the control room for launching agents on a task and watching their runs. This page is for whoever sits in front of it: which views exist, what every key does, and what a launch actually executes. The code lives in `internal/tui/board` (one file per concern) and the key map every view binds against is `internal/tui/common/keys.go`.

`pm` with no arguments and `pm board [project]` are the same thing. With no project argument the board opens on the project whose `path` contains the current directory, or on the first tab when none matches. The screen re-reads the task files every 2 seconds (`refreshInterval` in `internal/tui/board/types.go`), so an edit from an MCP session or from `$EDITOR` shows up on its own. Mouse cell motion is on, and the alternate screen is used, so `q` restores the terminal.

## Views

The `view` enum in `internal/tui/board/types.go` has eight members. Detail and project info remember where they were opened from and return there; the executor and runs views keep their own return view, so opening a run from a detail screen and closing it lands back on that detail screen.

| view | what it shows | enter | leave |
|---|---|---|---|
| board | status columns for the current project (or every project in the ALL tab), cards sorted by `order` then `updated` | default | `q` twice |
| detail | one task: the frontmatter head, the run dashboard when the task has one, the body with Spec and Log headers, subtask table for a tracker | `o`, `enter`, `space` on a card | `o`, `q`, `esc` |
| archive | the project's archived tasks | `ctrl+a` | `esc`, `ctrl+a` |
| project info | project.yaml rendered: path, stack, repo, notes, links, statuses | `i` | `o`, `esc`, `q` |
| focus | today's focus plan (`focus.yaml`) as a list | `T` | `esc`, `T` |
| executor | the live transcript of a task's run or acceptance, one tab per worker session | `W` on a task or a run row, `enter` in the runs view | `esc`, `q`, `o` |
| runs | every executor run and acceptance across projects, local and remote | `R` | `esc`, `q`, `R` |
| report | the acceptance report markdown (`<tracker>.finish.md`) | `F` in detail or in the runs view | `esc`, `q`, `o`, `F` |

## Keys

The shared key map, in the order `internal/tui/common/keys.go` declares it. "where" says which views honour the binding; a view not listed ignores the key.

| key | action | where |
|---|---|---|
| `↑` / `k` | move up | every list |
| `↓` / `j` | move down | every list |
| `←` / `h` | previous column | board |
| `→` / `l` | next column | board |
| `enter` | open detail (in the runs view: open the agent view) | board, archive, focus, runs, pickers |
| `o` | open / close detail | board, detail, archive, focus, runs, executor, report |
| `space` | toggle (detail from a card, hide in the project picker) | board, archive, focus, runs, project picker |
| `m` | move task forward one status | board, detail, focus, select mode |
| `M` | move task back one status | board, detail, focus, select mode |
| `d` | mark done (confirm) | board, detail, focus, select mode |
| `a` | add a task (title, then id) | board |
| `e` | edit the file in `$EDITOR` (the report in the report view) | board, detail, report |
| `L` | links menu | board, detail, project info |
| `tab` | next project | board, archive |
| `shift+tab` | previous project | board, archive |
| `/` | search; `-<text>` matches ids only | board, help; in detail: search inside the body |
| `q` | quit (confirm) / back | everywhere |
| `esc` | back / clear the filter | everywhere |
| `?` | help, filterable with `/` | board |
| `X` | delete task (confirm) | board, archive, select mode; remove from focus in the focus view |
| `g` | jump to top | every list |
| `G` | jump to bottom | every list |
| `y` | yank the task id to the clipboard | board, detail, focus, select mode, pickers |
| `Y` | yank menu: branch, id, title, file path, last session, every link | board, detail, focus, select mode |
| `A` | archive task (confirm) | board, detail, select mode |
| `ctrl+a` | archive view on / off | board, archive |
| `r` | restore (archive view); refresh elsewhere | archive, board, detail, runs, executor, report |
| `i` | project info | board, project picker |
| `u` | undo the last status change | board, archive, focus |
| `w` | mark waiting (confirm) | board, detail, focus, select mode |
| `v` | select mode (multi-select); verbose in the executor view | board, executor |
| `V` | column visibility menu | board |
| `ctrl+d` | half page down | board, runs, report |
| `ctrl+u` | half page up | board, runs, report |
| `;` | zoom toggle (one column full width) | board, focus |
| `ctrl+k` | reorder the task up (scroll up 10 in the executor view) | board, focus, project picker, executor |
| `ctrl+j` | reorder the task down (scroll down 10 in the executor view) | board, focus, project picker, executor |
| `c` | launch menu for Claude Code (cycle agents inside it with `@`) | board, detail, project info, focus, project picker |
| `x` | launch menu for the executor (`pm work` / `pm run-epic`) | board, detail |
| `W` | watch the task's run; inside the executor view: switch between the run and its acceptance | board, detail, executor |
| `K` | kill a live run (confirm) | board, detail, executor |
| `F` | acceptance report | detail, runs, report |
| `P` | project picker | board |
| `t` | toggle the task on today's focus plan (in the runs view: open the task's detail) | board, focus, runs |
| `T` | focus view | board, focus |
| `R` | runs view | board, runs |

Not in the shared map but bound in place:

| key | action | where |
|---|---|---|
| `1`-`9` | jump to project N | board |
| `p` | relation jump: a subtask opens its parent, a tracker opens the subtask picker | detail |
| `s` | session menu (resume, fork, yank, delete a recorded session) | detail |
| `n` / `N` | next / previous match of the body search | detail |
| `f` | fetch remote runs (runs view); follow on / off (executor view) | runs, executor |
| `tab` / `shift+tab` | switch worker session | executor |

Clipboard: `pbcopy` on macOS, `xclip -selection clipboard` or `xsel --clipboard --input` on Linux, first one found (`internal/tui/board/clipboard.go`).

## Per-view notes

**Board.** The status bar reads `m/M move  d done  a add  c claude  x exec  W watch  R runs  e edit  y/Y yank  L links  i info  C-a archived`, with `pm <version> <startup ms>` on the left. A search filter shows as `filter: "..." (/ to edit, esc to clear)` above it. Tabs carry the non-archived task count per project. `a` asks for a title, then for an id (empty = mint from the project's prefix). `ctrl+j` / `ctrl+k` swap the `order` of two neighbouring cards in a column; when every card still has `order: 0`, sequential orders are assigned first.

**Detail.** The footer reads `o/q: back  e: edit  r: refresh  m/w/d/A: move/wait/done/archive  c: claude  x/W/K: exec run/watch/stop  F: report  y/Y: yank  L: links  <pct>`; on project info it is `o/esc/q: back  ↑/↓/j/k scroll  c: claude  y/Y: yank  L: links  <pct>`. The head renders the run dashboard when the task has a run-state: status, heartbeat age, per-sub outcomes. `r` reloads the whole task set first so a tracker's subtask table reflects fresh child statuses. The Spec and Log markers in the body render as headers (`bodyToDisplayMarkdown` in `internal/tui/board/view_detail.go`).

**Archive.** Footer: `archived: N  ↑/↓ navigate  r restore  u undo  X delete  o detail  esc back`. `r` moves a task back to the first project status.

**Focus.** Footer: `focus: N  ↑/↓ navigate  t/X remove  C-j/C-k reorder  m move  d done  o detail  ; zoom  esc back`. The list is today's `focus.yaml`; reordering writes the plan back.

**Select mode.** `v` marks cards; the bar becomes ` SELECT: N selected ` with `v toggle  m/M move  d done  w wait  A archive  X del  y yank  Y menu  esc`. The status moves apply to every selected card; bulk delete asks `press X again to delete N tasks`.

**Runs.** Footer: `↑/↓ navigate · enter agent-view · t task · F report · f fetch remote · r refresh · esc back`. One row per tracker with its run state (`prepped`, `running N/M`, `done N/M`, `failed N/M`, `stale N/M`) and its acceptance state. `r` re-reads local files only; `f` starts the remote fetch (see below).

**Executor view.** The footer is `<worker> v <mode> · f follow:<on/off> · j/k C-j/k scroll · r · K kill · W <other run kind> · esc`; `K kill` appears only while the run is live, `W ...` only when the task also has the other kind of run (a run and its acceptance are routinely live together). `v` switches between the compact and the verbose rendering of the transcript, `f` pins the viewport to the tail, tabs switch between worker sessions of one run. With a kill armed the footer turns into `press K again to STOP this run (parks the worker on waiting | releases the acceptance claim) · any other key cancels`.

**Report.** Footer: `e: $EDITOR · j/k C-d/C-u scroll · r reload · esc back`. The header shows the report path; `e` opens that file.

## Confirm twice and undo

Six actions need the same key twice: `d` done, `w` waiting, `X` delete, `A` archive, `q` quit, `K` kill. The first press arms the action and prints a red prompt on the line above the status bar (`press d again to mark done`, `press K again to stop the run` naming which run); any other key disarms it (`internal/tui/board/update.go`). The kill prompt names the run it targets because a run and its acceptance are one keystroke apart and otherwise look the same.

`u` undoes the last status mutation from the board, archive or focus view by writing back a snapshot of the task taken before the change (`snapshotTask` and `doUndo` in `internal/tui/board/actions.go`). It restores the file wholesale, `status_changed` included, which is what undoing a move means: it is the one status write that deliberately does not re-stamp. Only the last action is kept; a second `u` says `nothing to undo`.

## Overlays

Every menu is an entry in one ordered table, `overlayLadder` in `internal/tui/board/overlay.go`, that both `View` and `Update` resolve through: the first open overlay in the list owns the screen and the keyboard. The order is launch menu, yank menu, links menu, session menu, subtask picker, column visibility, project picker, help. `esc`, `q` and `←` close any of them.

| overlay | opens with | keys |
|---|---|---|
| launch menu | `c`, `x` | see the next section |
| yank menu | `Y` | `↑`/`↓`, `enter` copies the item |
| links menu | `L` | `enter` opens the url with `open`, `y` copies it |
| session menu | `s` in detail | `enter` or `r` resume, `f` fork into a new session id, `y` yank the id, `x` delete the recorded session (confirm twice) |
| subtask picker | `p` on a tracker | `↑`/`↓`, `enter`/`o`/`→` open the child's detail |
| column visibility | `V` | toggle which status columns render |
| project picker | `P` | `enter` switch, `space` hide, `ctrl+j`/`ctrl+k` reorder, `/` filter, `y` path, `Y` yank menu, `c` launch, `i` info |
| help | `?` | `/` filters the 44-row table |

The add flow (`a`) is a two-step prompt rather than an overlay: title, then id.

## Launching agents

`c` and `x` open the same launch menu (`internal/tui/board/launch_menu.go`) with a different agent selected. The menu lists launch *kinds* for the selected task (or for the project when opened from the picker or project info), shows the argv it is about to run, and takes five toggles:

| toggle | effect | applies to |
|---|---|---|
| `!` | skip permissions (`--dangerously-skip-permissions` for claude, `--dangerously-bypass-approvals-and-sandbox` for codex, `--yolo` for the executor) | every agent |
| `#` | run in an additional worktree slot (`--additional`), when project.yaml declares one | executor |
| `&` | chain the acceptance after the run (`--then-finish`) | executor, tracker only |
| `$` | let the acceptance drive the simulator (`--sim`); accepted only when the tmux acceptance is on the menu | executor, tracker in tmux |
| `@` | cycle the agent: claude, codex, executor (project scope: claude, codex) | menu |

The tmux kinds exist only when the board itself runs under tmux (`$TMUX` set); then the tmux variant is the default cursor position. What each kind executes:

| agent | kind | key | executes |
|---|---|---|---|
| claude | here | `h` | `claude --session-id <new uuid> [skip] <prompt>` in the project directory, board suspended |
| claude | tmux window | `t` | the same in a new tmux window named `cc:<sess4>:<task id>` |
| claude | worktree | `w` | `claude -w <name> --session-id <uuid> [skip] <prompt>`; the worktree is created first under `<project>/.claude/worktrees/<name>` and untracked files are copied in |
| claude | worktree tmux | `W` | the worktree variant in a tmux window named `wt:<sess4>:<task id>` |
| claude | resume | `r` | `claude --resume <last session id>` (only when the task has a recorded session) |
| claude | resume tmux | `R` | the same in a tmux window |
| claude | fork | `h` in the fork submenu | `claude --resume <parent> --fork-session --session-id <new>` |
| claude | project scope | `p` | `claude --session-id <uuid> [skip] <project prompt>` with no task |
| codex | here / tmux / worktree / worktree tmux / project | `h` `t` `w` `W` `p` | `codex [bypass] <prompt>`; the tmux window is `cx:<task id>` and a non-zero exit pauses with `Press Enter to return to board` |
| executor | background | `b` | `pm work <project> <task> [--yolo] [--additional]` detached (own session id, log under `.executor/`, a run-state seeded so the board shows it at once); a tracker runs `pm run-epic ... [--then-finish]` |
| executor | here | `h` | the same in the foreground, board suspended |
| executor | tmux window | `t` | the same in a tmux window |
| executor | now, detached | `a` | `pm finish <tracker> --project <slug> --no-sim [--additional]` detached |
| executor | now, in a tmux window | `A` | `pm finish ... --sim|--no-sim` in a tmux window; the only launch that may carry `--sim` |
| executor | dry-run preview | `d` | `pm work|run-epic ... --dry-run`: the prompt and command, nothing spawned |

The executor menu names a tracker's run an "epic" or a "batch" from its `epic_mode` (the board never passes `--independent`; the tracker declares it), shows the worktree slots with their live holders, and warns when a run or an acceptance of the same task is already live. A launch on a task hands the agent a prompt built from the task: `Working on: #<id> <title> [<status>]`, project path, stack, repo, notes, the brief, the acceptance criteria and the body (`buildClaudePrompt` in `internal/tui/board/launch_claude.go`). A new session id is appended to the task's `sessions`, which is what `resume` and `fork` read back.

Two more details a launch carries:

- `CLAUDE_CONFIG_DIR` is set when the project's `claude_config_dir` differs from the default, and `executor.env` plus the slot's env are prefixed on tmux launches as `KEY=value` (keys validated as shell identifiers, values quoted).
- A worktree launch claims its slot through a session lock at `<project data dir>/.sessions/slot-<n>.lock`, `{"pid","session_id","slot","started"}`, written by the launched shell itself with a tmp-and-rename so a rival never reads a torn file (`sessionLockScript`). This is the interactive twin of the executor's `.pm-executor.lock` in the worktree: the two never share a slot.

The worktree name is the task's `branch`, else its slugified title (`worktreeName` in `internal/tui/board/worktree.go`), so a launch on "Enable analytics" with `branch: feat/enable-analytics` lands in a worktree of that name. A failed `git worktree add` aborts the launch instead of running in the main checkout.

## Runs from other machines

The runs view starts local. `f` re-executes the pm binary itself as `pm runs --json` in its own process group and merges the rows it prints, remotes included: every runner declared under `remotes:` in `config.yaml` is asked over ssh (`internal/tui/board/update_runs.go`). The whole fetch is bounded at 2 minutes, each remote at the 20 seconds `pm runs` itself allows; a remote that does not answer costs one note row, never the local rows. The fetch is asynchronous, so the board keeps ticking while ssh negotiates.

## tui-config.yaml

`<pm data dir>/tui-config.yaml` holds the board's own state, written by the project picker (`internal/tui/board/config.go`):

```yaml
hidden_projects: [orbit]        # space in the picker
project_order: [acme, vega]     # ctrl+j / ctrl+k in the picker
```

It is UI state and nothing else: a hidden project still counts everywhere the CLI, the MCP server or the cockpit look. The fact that a project is asleep is `archived: true` in its `project.yaml`, which takes it out of every cross-project view. An unreadable `tui-config.yaml` degrades to the zero config, and the file is written plainly (no merge, no comments kept).
