package storage

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// signalRecorder swaps out the real syscall.Kill for the duration of a test, so
// what a kill WOULD signal can be asserted without a test process ever signalling
// a live process group.
type signalRecorder struct {
	calls []int         // targets, in order (negative = process group)
	fail  map[int]error // targets that should report an error
}

func (r *signalRecorder) install(t *testing.T) {
	t.Helper()
	prev := killSignal
	killSignal = func(pid int, _ syscall.Signal) error {
		r.calls = append(r.calls, pid)
		return r.fail[pid]
	}
	t.Cleanup(func() { killSignal = prev })
}

// The whole point of the ticket: a run-state is a file that outlives its
// process, so a pid read out of one proves nothing on its own. pid 1 is alive
// everywhere and did not start in 2019 - the recycled-number shape.
func TestKillRefusesAnUnprovenPID(t *testing.T) {
	rec := &signalRecorder{}
	rec.install(t)

	st := &RunState{TaskID: "p-9", PID: 1, WorkerPGID: 1234, Started: "2019-01-01T00:00:00Z"}
	var stale *StaleRunError
	err := st.Kill(syscall.SIGTERM)
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a StaleRunError", err)
	}
	if stale.PID != 1 || stale.TaskID != "p-9" {
		t.Errorf("stale error must name the run and the pid, got %+v", stale)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("nothing may be signalled without proof, got %v", rec.calls)
	}
}

func TestKillSignalsAProvenRun(t *testing.T) {
	self, parent := os.Getpid(), os.Getppid()
	now := time.Now().UTC().Format(time.RFC3339)

	t.Run("group first, worker before manager", func(t *testing.T) {
		rec := &signalRecorder{}
		rec.install(t)

		// WorkerPGID stands in for the in-flight worker's own group. Order is
		// load-bearing: SIGKILLing the manager is what makes the worker
		// unreachable, so the worker's group must be signalled first.
		st := &RunState{TaskID: "p-1", PID: self, WorkerPGID: parent, Started: now}
		if err := st.Kill(syscall.SIGTERM); err != nil {
			t.Fatalf("Kill() = %v, want nil", err)
		}
		want := []int{-parent, -self}
		if len(rec.calls) != len(want) || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
			t.Errorf("signalled %v, want %v", rec.calls, want)
		}
	})

	t.Run("falls back to the bare pid when the group send fails", func(t *testing.T) {
		rec := &signalRecorder{fail: map[int]error{-self: syscall.ESRCH}}
		rec.install(t)

		st := &RunState{TaskID: "p-1", PID: self, Started: now}
		if err := st.Kill(syscall.SIGTERM); err != nil {
			t.Fatalf("Kill() = %v, want nil", err)
		}
		want := []int{-self, self}
		if len(rec.calls) != len(want) || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
			t.Errorf("signalled %v, want %v", rec.calls, want)
		}
	})

	t.Run("a dead worker group is not signalled", func(t *testing.T) {
		rec := &signalRecorder{}
		rec.install(t)

		st := &RunState{TaskID: "p-1", PID: self, WorkerPGID: 2147483646, Started: now}
		if err := st.Kill(syscall.SIGTERM); err != nil {
			t.Fatalf("Kill() = %v, want nil", err)
		}
		if len(rec.calls) != 1 || rec.calls[0] != -self {
			t.Errorf("signalled %v, want only the manager group", rec.calls)
		}
	})

	t.Run("no pid at all", func(t *testing.T) {
		rec := &signalRecorder{}
		rec.install(t)

		if err := (&RunState{}).Kill(syscall.SIGTERM); err == nil {
			t.Error("a run-state with no pid must report an error")
		}
		if len(rec.calls) != 0 {
			t.Errorf("nothing to signal, got %v", rec.calls)
		}
	})
}
