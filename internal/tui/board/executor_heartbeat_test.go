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
	if got := execHeartbeatAge(time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)); got != "1m30s ago" {
		t.Errorf("age = %q, want \"1m30s ago\"", got)
	}
	// Clock skew between the writing executor and the reading board must not
	// render a negative age.
	if got := execHeartbeatAge(time.Now().Add(time.Minute).UTC().Format(time.RFC3339)); got != "0s ago" {
		t.Errorf("future stamp = %q, want \"0s ago\"", got)
	}
}

func TestDashboardShowsHeartbeatOnlyWhenLive(t *testing.T) {
	// Live: own pid + status running, stamped a moment ago.
	live := &storage.RunState{
		TaskID: "p-1", Kind: "run-epic", Status: storage.RunStatusRunning, PID: os.Getpid(),
		Started: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		Updated: time.Now().Add(-20 * time.Second).UTC().Format(time.RFC3339),
		Subs:    []storage.SubRun{{ID: "p-1-1", Status: storage.RunStatusRunning}},
	}
	if out := stripANSI(renderExecutorDashboard(live, 100)); !strings.Contains(out, "♥ 20s ago") {
		t.Errorf("live run should show the heartbeat age\n---\n%s", out)
	}

	// Finished: Updated IS the end stamp there, so showing it as a heartbeat
	// age would read as a run that just beat.
	done := *live
	done.Status = storage.RunStatusDone
	if out := stripANSI(renderExecutorDashboard(&done, 100)); strings.Contains(out, "♥") {
		t.Errorf("finished run must not show a heartbeat\n---\n%s", out)
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
