package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/mbalazy/pm/internal/storage"
)

// Auto-chain: `pm run-epic` spawning the odbiór of its own run on the way out.
//
// There is no daemon and no watcher behind this, deliberately. The manager
// process lives for the whole run and is therefore the one thing in the system
// that KNOWS the run is over - anything else would have to poll state to find
// out something the manager already had. So the chain is a single detached
// spawn at the end of executeEpic: launch a batch at night, and the acceptance
// has run by morning.
//
// SAME MACHINE ONLY, and that is a limitation rather than an oversight: the
// chain is this process spawning a child, so a run on the VPS chains an
// acceptance on the VPS. The one scenario people ask about - batch on the VPS,
// acceptance with a simulator on the mac - is impossible in that direction
// anyway (the mac sits behind NAT with no exposed ssh, so the VPS cannot reach
// it), and it is the scenario where a human is present regardless. The reverse
// direction (mac chaining onto the VPS over ssh) would be easy, and is left out
// on purpose: it is the direction nobody needs.

// finishChainExecutable resolves the pm binary to chain into. It is
// os.Executable and NOT the string "pm": a manager on the VPS may well be
// running from a path that is not the one on the runner's PATH (or from no PATH
// entry at all), and chaining into a DIFFERENT pm build than the one that just
// drove the run is the kind of mismatch that shows up as an unexplained
// acceptance failure at 3am. Overridden in tests.
var finishChainExecutable = os.Executable

// chainFinish spawns the detached acceptance run for a finished epic, if the
// run asked for one. It is BEST-EFFORT in the strong sense: every failure path
// returns after a single stderr line, because the epic has already succeeded
// and its result must not depend on anything that happens after it.
//
// Returns whether an acceptance was actually started (for the caller's own
// reporting and for tests); the caller ignores it in production.
func chainFinish(errOut io.Writer, stateDir, slug, trackerID, workDir string) bool {
	// A claim already held is the NORMAL case, not a failure: `batch-finish-auto`
	// is built to start before a run ends and accept subs as they land, so a
	// session doing exactly that holds the claim while this run finishes. One
	// line, no error - the acceptance is already happening, which is the outcome
	// the chain wanted.
	if holder := storage.LiveFinishClaimHolder(stateDir, trackerID); holder != nil {
		fmt.Fprintf(errOut, "pm run-epic: not chaining the odbiór of %s - it is already claimed by %s (pid %d) since %s\n",
			trackerID, holder.Host, holder.PID, holder.Started)
		return false
	}

	exe, err := finishChainExecutable()
	if err != nil {
		fmt.Fprintf(errOut, "pm run-epic: not chaining the odbiór of %s - cannot resolve the pm binary: %v\n", trackerID, err)
		return false
	}

	logPath := storage.FinishRunLogPath(stateDir, trackerID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		fmt.Fprintf(errOut, "pm run-epic: not chaining the odbiór of %s - cannot create %s: %v\n", trackerID, filepath.Dir(logPath), err)
		return false
	}
	logf, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(errOut, "pm run-epic: not chaining the odbiór of %s - cannot open the acceptance log %s: %v\n", trackerID, logPath, err)
		return false
	}
	defer logf.Close() // the child keeps its own dup'd fd

	// --no-sim is passed explicitly even though it is the default: anything
	// running detached keeps its hands off a shared runtime, and saying so in
	// the argv is what makes that visible in the log and in `ps`.
	// --project is passed rather than relying on cwd detection, so the chained
	// run resolves the same project this one ran, whatever workDir turns out to
	// be (a claimed worktree slot is not the path in project.yaml).
	c := exec.Command(exe, "finish", trackerID, "--project", slug, "--no-sim")
	c.Dir = workDir
	c.Stdout = logf
	c.Stderr = logf
	// Detached: the acceptance outlives this manager (it is longer than the run
	// in the normal case) and must not die with the terminal the run was
	// launched from.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		fmt.Fprintf(errOut, "pm run-epic: could not start the odbiór of %s: %v (the run itself is unaffected - accept it by hand with `pm finish %s`)\n",
			trackerID, err, trackerID)
		return false
	}
	fmt.Fprintf(errOut, "pm run-epic: chained the odbiór of %s - detached `pm finish` (pid %d), log: %s\n",
		trackerID, c.Process.Pid, logPath)
	return true
}
