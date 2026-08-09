package board

import (
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// What an acceptance leaves behind. A detached acceptance settles everything it
// can read and nothing it would have to LOOK at, so a clean "done" routinely
// hides a queue of visual checks. The board's job here is to say that the queue
// exists - the Runs view already carries the number, but only for somebody who
// already suspects there is something to see.

// doneFinish is an acceptance that ended, with n visual claims left open. The
// pid is deliberately one that cannot be live (0), because the whole case under
// test is the state stateBadge renders as nothing at all.
func doneFinish(taskID string, n int) *storage.RunState {
	return &storage.RunState{
		TaskID: taskID, Project: "p", Kind: storage.RunKindFinish, Status: storage.RunStatusDone,
		Started: time.Now().UTC().Format(time.RFC3339),
		Subs:    []storage.SubRun{{ID: taskID, Status: "done", VisualClaimsOpen: n}},
	}
}

func TestFinishBadgeSurfacesOpenVisualClaims(t *testing.T) {
	cases := []struct {
		name    string
		state   *storage.RunState
		want    string
		wantNot string
	}{
		{
			name:  "a finished acceptance with claims left says how many",
			state: doneFinish("p-9", 7),
			want:  "👁 7 to review",
		},
		{
			// The old behaviour, and still correct: a clean acceptance needs no
			// badge, the task's own status has moved on.
			name:    "a finished acceptance with nothing open stays silent",
			state:   doneFinish("p-9", 0),
			wantNot: "👁",
		},
		{
			// Both halves: what happened to the run, and what it left behind.
			name: "a failed acceptance still reports what it counted",
			state: func() *storage.RunState {
				st := doneFinish("p-9", 2)
				st.Status = storage.RunStatusFailed
				return st
			}(),
			want: "✗ accept failed 👁 2 to review",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
			if err := storage.WriteRunState(m.store.ProjectDir("p"), tc.state); err != nil {
				t.Fatal(err)
			}
			m.refreshRunStates()

			got := m.finishBadge("p-9")
			if tc.want != "" && got != tc.want {
				t.Errorf("badge = %q, want %q", got, tc.want)
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Errorf("badge = %q, must not mention %q", got, tc.wantNot)
			}
			// The badge is only worth anything if it reaches the card - the whole
			// point is being seen without opening anything.
			if tc.want != "" && !strings.Contains(stripANSI(m.View()), "👁 ") {
				t.Errorf("the count never reached the card:\n%s", m.View())
			}
		})
	}
}

// A live acceptance is mid-count: its number is a snapshot of a run that has not
// decided yet, so the live word stands alone rather than being suffixed with a
// figure that is about to change.
func TestALiveAcceptanceIsNotBadgedWithAPartialCount(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	st := liveFinish(t, "p-9", liveRunPID(t))
	st.Subs[0].VisualClaimsOpen = 3
	if err := storage.WriteRunState(m.store.ProjectDir("p"), st); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()

	if got := m.finishBadge("p-9"); got != "▶ accepting" {
		t.Errorf("badge = %q, want the live word alone", got)
	}
}

// The board and `pm runs` must count by one rule: the number is a TODO list, and
// two places disagreeing about its length is worse than showing it in neither.
func TestTheBoardAndTheRunsTableCountTheSameClaims(t *testing.T) {
	st := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: storage.RunKindFinish, Status: storage.RunStatusDone,
		Subs: []storage.SubRun{
			{ID: "p-9-1", Status: "done", VisualClaimsOpen: 2},
			{ID: "p-9-2", Status: "done"},
			{ID: "p-9-3", Status: "done", VisualClaimsOpen: 5},
		},
	}
	if got := st.VisualClaimsOpen(); got != 7 {
		t.Errorf("sum = %d, want 7", got)
	}
	// A state whose kind was never checked, and a nil one, answer truthfully
	// rather than defensively - callers hold both.
	if got := (*storage.RunState)(nil).VisualClaimsOpen(); got != 0 {
		t.Errorf("nil sum = %d, want 0", got)
	}
	if got := (&storage.RunState{}).VisualClaimsOpen(); got != 0 {
		t.Errorf("empty sum = %d, want 0", got)
	}
}
