package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mbalazy/pm/internal/storage"
)

// executorPMArgs builds the `pm` argv for the executor launch: `work` for a leaf
// task, `run-epic` for a tracker, plus optional --yolo / --dry-run. Pure so it
// can be unit-tested without spawning a process.
func executorPMArgs(t *storage.Task, isTracker, yolo, dryRun, additional, thenFinish bool) []string {
	// The subcommand name doubles as the run-state's Kind (see the seed below),
	// and Kind now decides which FILE the run-state lives in - so these are the
	// storage constants rather than two literals that merely look the same.
	sub := storage.RunKindWork
	if isTracker {
		sub = storage.RunKindEpic
	}
	args := []string{sub, t.Project, t.Meta.ID}
	if yolo {
		args = append(args, "--yolo")
	}
	if additional {
		args = append(args, "--additional")
	}
	// --then-finish exists only on `pm run-epic`; appending it to `pm work`
	// would be a flag-parse error, so the tracker check lives here rather than
	// trusting the caller's toggle state.
	if thenFinish && isTracker {
		args = append(args, "--then-finish")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

// finishPMArgs builds the `pm` argv for launching a tracker's acceptance from
// the board. Mirrors chainFinish's spawn (run_epic_finish.go): --project
// because `pm finish` takes only the tracker positionally, and --no-sim
// explicitly even though it is the default - a board launch is detached, and a
// detached acceptance keeps its hands off a shared runtime; saying so in the
// argv makes that visible in the log and in `ps`. No --yolo handling: the
// acceptance is yolo by default (the menu's ! toggle does not apply). Pure for
// the same unit-test reason as executorPMArgs.
func finishPMArgs(t *storage.Task, additional bool) []string {
	args := []string{storage.RunKindFinish, t.Meta.ID, "--project", t.Project, "--no-sim"}
	if additional {
		args = append(args, "--additional")
	}
	return args
}

// liveRunForLaunch returns the warning to show when a launch of this kind would
// land on top of one already in flight for t, or "" when the coast is clear.
// The two kinds are asked about SEPARATELY, never as "is anything running":
// a run and its acceptance are routinely live together by design, so warning
// about the run when the user pressed [a] would be an alarm about the normal
// state - and the two occupy different files, so neither disturbs the other.
func (m Model) liveRunForLaunch(t *storage.Task, isFinish bool) string {
	if isFinish {
		if m.finishStates[t.Meta.ID].IsLive() {
			return "⚠ an acceptance of this tracker is already running (W to watch, K to stop); the new one will refuse on the claim"
		}
		return ""
	}
	if m.runStates[t.Meta.ID].IsLive() {
		return "⚠ a run of this task is already in flight (W to watch, K to stop); nothing stops two managers from working the same branch"
	}
	return ""
}

// openRunLog opens a detached run's log. Fresh launches TRUNCATE, which is what
// makes the log the record of one run rather than of every run this task ever
// had. When a run of the same kind is already live, though, that same truncate
// empties the log the live process is writing - and it holds its own fd, so it
// keeps writing at its old offset and leaves a file that begins with a hole.
// There the log is opened for APPEND instead: two runs interleaving in one file
// is confusing, a live run's record being blanked is lost.
func openRunLog(path string, live bool) (*os.File, error) {
	if live {
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}
	return os.Create(path)
}

// pmShellCommand renders `cd <dir> && pm <args...>` with each token shell-quoted.
func pmShellCommand(dir string, args []string) string {
	parts := []string{"pm"}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(dir), strings.Join(parts, " "))
}

// Pause tails keep the terminal/tmux window readable after the executor exits.
// execPauseOnError waits only on a non-zero exit (used for "here" runs that
// return to the board); execPauseAlways always waits (tmux windows and dry-run
// previews, where the output is the whole point).
const execPauseOnError = "; status=$?; if [ $status -ne 0 ]; then printf '\\n[pm] executor exited with status %s. Press Enter to return to board...' \"$status\"; read _; fi; exit $status"
const execPauseAlways = "; status=$?; printf '\\n[pm] executor exited (status %s). Press Enter to close...' \"$status\"; read _"

// boardGitPreflight returns a human-readable reason the executor cannot run in
// dir, or "" if the preconditions are satisfied. dir must be a git repo; the
// clean-tree check is only enforced when requireClean is true. In worktree mode
// the executor runs in a separate "additional" worktree, so the user's main
// checkout is allowed to be dirty (requireClean=false).
func boardGitPreflight(dir string, requireClean bool) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return "not a git repo: " + dir
	}
	if requireClean {
		st, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
		if err == nil && strings.TrimSpace(string(st)) != "" {
			return "working tree dirty - commit or stash first"
		}
	}
	return ""
}

// launchExecutor runs `pm work`/`pm run-epic` for the menu task. kind is one of
// "here", "tmux", "dry-run". Reuses the launch overlay's skip-perms toggle as
// --yolo. Long runs default to tmux; the board reflects the resulting
// status/brief on reload (launchResultMsg triggers m.reload()).
func (m Model) launchExecutor(kind string) (tea.Model, tea.Cmd) {
	t := m.menuTask()
	if t == nil {
		return m, nil
	}

	// Resolve project working directory.
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}
	if projDir == "" {
		m.toastMsg = "executor: project has no valid path - set it in project.yaml"
		m.toastExpiry = time.Now().Add(15 * time.Second)
		return m, nil
	}

	isTracker := len(m.taskChildren(t)) > 0
	dryRun := kind == "dry-run"
	isFinish := kind == "finish"
	// The additional-worktree choice is per launch and only valid when the project
	// has it configured (executorAdditionalAvail, set while building the menu).
	additional := m.claudeMenuAdditional && m.executorAdditionalAvail
	args := executorPMArgs(t, isTracker, m.claudeMenuSkipPerms, dryRun, additional, m.claudeMenuThenFinish)
	if isFinish {
		args = finishPMArgs(t, additional)
	}
	winName := "pm:" + args[0] + ":" + t.Meta.ID

	// Real runs (here/tmux) require a git repo; a clean tree is only required in
	// DEFAULT mode (the executor runs in the main checkout, old behaviour). With
	// --additional the work is isolated in the worktree, so the main checkout may
	// be dirty. Dry-run is side-effect-free. An acceptance never needs a clean
	// tree either: it forks no branch of its own (it walks the subs' branches),
	// and the epic it accepts may well have left the checkout mid-state -
	// chainFinish spawns it with no preflight at all for the same reason.
	if !dryRun {
		if reason := boardGitPreflight(projDir, !additional && !isFinish); reason != "" {
			m.toastMsg = "executor: " + reason
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
	}

	// A run of the same kind may already be in flight for this task. That is
	// WARNED about, not blocked: the board's run-state maps are up to a tick
	// old, so a block here would be a race deciding whether a launch happens at
	// all, and the authority on a second acceptance is the claim in the pm
	// process (a second `pm finish` refuses on it and costs nothing but a
	// message). What the warning cannot leave to chance is the LOG: the second
	// launch writes to the same path, so the truncation below would empty the
	// live run's log under it - see appendLog.
	warn := ""
	if !dryRun {
		if live := m.liveRunForLaunch(t, isFinish); live != "" {
			warn = live
		}
	}

	switch kind {
	case "bg", "finish":
		// Detached background run, output to a log file, observable natively in
		// pm (run-state + agent-view). Does not take over the terminal or need tmux.
		// Run-state + log live in the pm data dir (where the board reads them);
		// the worker still runs in the git repo (c.Dir = projDir).
		stateDir := m.store.ProjectDir(t.Project)
		logPath := storage.ExecutorLogPath(stateDir, t.Meta.ID)
		if isFinish {
			// The acceptance's artifacts are SIBLINGS of the tracker's run, not
			// replacements: ExecutorLogPath here would overwrite the log of the
			// very run being accepted. The run-state seed below routes itself
			// (WriteRunState picks the .finish.json path off Kind == args[0] ==
			// storage.RunKindFinish).
			logPath = storage.FinishRunLogPath(stateDir, t.Meta.ID)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			m.toastMsg = "executor: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		logf, err := openRunLog(logPath, warn != "")
		if err != nil {
			m.toastMsg = "executor: cannot open log: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		c := exec.Command("pm", args...)
		c.Dir = projDir
		c.Stdout = logf
		c.Stderr = logf
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach: survive board exit, no controlling tty
		err = c.Start()
		logf.Close() // the child keeps its own dup'd fd
		if err != nil {
			m.toastMsg = "executor: failed to start: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		// Seed run-state so the board reflects the run immediately; the executor
		// process overwrites it with richer progress (subs/phase/session) as it goes.
		// Best-effort: a failed seed just delays the first dashboard refresh.
		_ = storage.WriteRunState(stateDir, &storage.RunState{
			TaskID:   t.Meta.ID,
			Project:  t.Project,
			Kind:     args[0],
			Status:   storage.RunStatusRunning,
			PID:      c.Process.Pid,
			RepoPath: projDir,
			LogPath:  logPath,
			Started:  time.Now().UTC().Format(time.RFC3339),
		})
		m.refreshRunStates()
		m.toastMsg = fmt.Sprintf("Started %s %s in background (▶ on the board)", args[0], t.Meta.ID)
		m.toastExpiry = time.Now().Add(4 * time.Second)
		if warn != "" {
			// The warning outlives the "started" note it is appended to: the
			// launch succeeding is the unsurprising half, and 4 seconds is not
			// long enough to read why the run that follows may refuse.
			m.toastMsg = warn + " - " + m.toastMsg
			m.toastExpiry = time.Now().Add(15 * time.Second)
		}
		return m, nil

	case "here":
		c := exec.Command("sh", "-c", pmShellCommand(projDir, args)+execPauseOnError)
		c.Dir = projDir
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(winName)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentExecutor, kind: "here", err: err}
		})

	case "dry-run":
		c := exec.Command("sh", "-c", pmShellCommand(projDir, args)+execPauseAlways)
		c.Dir = projDir
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(winName + ":dry")
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentExecutor, kind: "dry-run", err: err}
		})

	case "tmux":
		sess, err := tmuxNewWindow(winName, pmShellCommand(projDir, args)+execPauseAlways)
		if err != nil {
			m.toastMsg = "executor tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched "+args[0]+" in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
			if warn != "" {
				m.toastMsg = warn + " - " + m.toastMsg
				m.toastExpiry = time.Now().Add(15 * time.Second)
			}
		}
	}

	return m, nil
}
