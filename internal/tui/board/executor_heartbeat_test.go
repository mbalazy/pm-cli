package board

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func TestExecHeartbeatAge(t *testing.T) {
	if got := execHeartbeatAge(""); got != "" {
		t.Errorf("missing stamp must render empty, got %q", got)
	}
	if got := execHeartbeatAge("not-a-time"); got != "" {
		t.Errorf("unparseable stamp must render empty, got %q", got)
	}
	// The stamp is truncated to a second, so the age can land one second later
	// if the sub-second remainder of now rolls over inside the call.
	if got := execHeartbeatAge(time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)); got != "1m30s ago" && got != "1m31s ago" {
		t.Errorf("age = %q, want \"1m30s ago\"", got)
	}
	// Clock skew between the writing executor and the reading board must not
	// render a negative age.
	if got := execHeartbeatAge(time.Now().Add(time.Minute).UTC().Format(time.RFC3339)); got != "0s ago" {
		t.Errorf("future stamp = %q, want \"0s ago\"", got)
	}
}

func TestDashboardShowsHeartbeatOnlyWhileAWorkerRuns(t *testing.T) {
	// A worker is in flight: a live pid, status running, session pinned.
	//
	// Liveness compares the holder's START TIME with the run's Started stamp (a
	// pid that started after the stamp is a recycled number, not our manager),
	// so the fixture must be a pid that demonstrably predates its own stamp.
	// Our OWN pid with a stamp of now is the only pair that holds everywhere:
	// pid 1 seemed older than any stamp a test could write, but on a CI runner
	// the VM boots minutes before the job, so systemd started AFTER a stamp
	// dated ten minutes ago and the run read as recycled (red on Linux, green
	// on a mac whose launchd has been up for days). Started only feeds the
	// elapsed clock here; the heartbeat age comes off Updated.
	live := &storage.RunState{
		TaskID: "p-1", Kind: "run-epic", Status: storage.RunStatusRunning, PID: os.Getpid(),
		Started:        time.Now().UTC().Format(time.RFC3339),
		Updated:        time.Now().Add(-20 * time.Second).UTC().Format(time.RFC3339),
		CurrentSub:     "p-1-1",
		CurrentSession: "sess-abc",
		Subs:           []storage.SubRun{{ID: "p-1-1", Status: storage.RunStatusRunning}},
	}
	if out := stripANSI(renderExecutorDashboard(live, 100)); !strings.Contains(out, "♥ 20s ago") {
		t.Errorf("a run with a worker in flight should show the heartbeat age\n---\n%s", out)
	}

	// No worker yet: this is the board's seed state, or the manager doing
	// worktree seeding / executor.prepare / baseline capture. Nothing beats in
	// that window, so an age here would read as "hung" on a healthy run.
	noWorker := *live
	noWorker.CurrentSub = ""
	noWorker.CurrentSession = ""
	if out := stripANSI(renderExecutorDashboard(&noWorker, 100)); strings.Contains(out, "♥") {
		t.Errorf("no worker in flight -> no heartbeat age\n---\n%s", out)
	}

	// Finished: Updated IS the end stamp there, so showing it as a heartbeat
	// age would read as a run that just beat.
	done := *live
	done.Status = storage.RunStatusDone
	if out := stripANSI(renderExecutorDashboard(&done, 100)); strings.Contains(out, "♥") {
		t.Errorf("finished run must not show a heartbeat\n---\n%s", out)
	}
}

// Clearing CurrentSession the moment a worker returns (so the heartbeat gate
// stays honest) must not send the agent-view back to the run's FIRST sub.
func TestDefaultSessionIdx(t *testing.T) {
	sessions := []execSession{
		{subID: "p-1-1", session: "sess-a"},
		{subID: "p-1-2", session: "sess-b"},
		{subID: "p-1-3", session: "sess-c"},
	}
	if got := defaultSessionIdx(sessions, "sess-b"); got != 1 {
		t.Errorf("a worker in flight should be selected: got %d, want 1", got)
	}
	if got := defaultSessionIdx(sessions, ""); got != 2 {
		t.Errorf("no worker in flight -> most recent transcript: got %d, want 2", got)
	}
	if got := defaultSessionIdx(sessions, "sess-gone"); got != 2 {
		t.Errorf("unknown session -> most recent transcript: got %d, want 2", got)
	}
	if got := defaultSessionIdx(nil, "sess-a"); got != 0 {
		t.Errorf("no sessions -> 0, got %d", got)
	}
}

func TestShortDur(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-5 * time.Second, "0s"},
		{9 * time.Second, "9s"},
		{95 * time.Second, "1m35s"},
		{3*time.Hour + 4*time.Minute, "3h4m"},
	}
	for _, tc := range tests {
		if got := shortDur(tc.d); got != tc.want {
			t.Errorf("shortDur(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
