package storage

import "testing"

func orderTask(id, parent string, order int) *Task {
	return &Task{Meta: TaskMeta{ID: id, Parent: parent, Order: order}, Project: "test"}
}

func TestBuildTrackersSortsChildrenByOrder(t *testing.T) {
	// Children given out of order, with explicit order values that do NOT match
	// the numeric ID sequence - the rollup must follow order, not ID.
	tasks := []*Task{
		orderTask("p-1", "", 0),
		orderTask("p-1-1", "p-1", 30),
		orderTask("p-1-2", "p-1", 10),
		orderTask("p-1-3", "p-1", 20),
	}
	trackers, _ := BuildTrackers(tasks)
	if len(trackers) != 1 {
		t.Fatalf("trackers = %d, want 1", len(trackers))
	}
	gotIDs := []string{}
	gotOrders := []int{}
	for _, c := range trackers[0].Children {
		gotIDs = append(gotIDs, c.ID)
		gotOrders = append(gotOrders, c.Order)
	}
	wantIDs := []string{"p-1-2", "p-1-3", "p-1-1"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("children[%d] = %q, want %q (full: %v)", i, gotIDs[i], wantIDs[i], gotIDs)
		}
	}
	// Order must be exposed on the child object.
	wantOrders := []int{10, 20, 30}
	for i := range wantOrders {
		if gotOrders[i] != wantOrders[i] {
			t.Errorf("children[%d].Order = %d, want %d", i, gotOrders[i], wantOrders[i])
		}
	}
}

func TestBuildTrackersOrderFallsBackToID(t *testing.T) {
	// Equal/zero order -> tiebreak by numeric ID ascending.
	tasks := []*Task{
		orderTask("p-2", "", 0),
		orderTask("p-2-10", "p-2", 0),
		orderTask("p-2-2", "p-2", 0),
		orderTask("p-2-1", "p-2", 0),
	}
	trackers, _ := BuildTrackers(tasks)
	want := []string{"p-2-1", "p-2-2", "p-2-10"}
	for i, c := range trackers[0].Children {
		if c.ID != want[i] {
			t.Errorf("children[%d] = %q, want %q", i, c.ID, want[i])
		}
	}
}

func TestBuildTrackersArchivedParentOrphansChild(t *testing.T) {
	tasks := []*Task{
		{Meta: TaskMeta{ID: "p-1", Status: StatusArchived}, Project: "test"},
		{Meta: TaskMeta{ID: "p-1-1", Parent: "p-1", Status: StatusDoing}, Project: "test"},
	}
	trackers, suppressed := BuildTrackers(tasks)
	if len(trackers) != 0 {
		t.Fatalf("trackers = %d, want 0 (archived parent emits no tracker block)", len(trackers))
	}
	if suppressed["p-1-1"] {
		t.Error("child of archived parent should NOT be suppressed - it must fall back to the flat list")
	}
	if suppressed["p-1"] {
		t.Error("archived parent itself should NOT be suppressed - no tracker block claims it")
	}
}

func TestBuildTrackersMissingParentOrphansChild(t *testing.T) {
	tasks := []*Task{
		{Meta: TaskMeta{ID: "p-1-1", Parent: "ghost-99", Status: StatusDoing}, Project: "test"},
	}
	trackers, suppressed := BuildTrackers(tasks)
	if len(trackers) != 0 {
		t.Fatalf("trackers = %d, want 0 (parent doesn't exist among tasks)", len(trackers))
	}
	if suppressed["p-1-1"] {
		t.Error("child of a nonexistent parent should NOT be suppressed - it must fall back to the flat list")
	}
}

func TestLessByOrder(t *testing.T) {
	cases := []struct {
		name string
		a, b *Task
		want bool
	}{
		{"order asc", orderTask("x-2", "", 10), orderTask("x-1", "", 20), true},
		{"unset before ordered", orderTask("x-1", "", 0), orderTask("x-2", "", 10), true},
		{"equal order -> numeric id", orderTask("x-2", "", 10), orderTask("x-10", "", 10), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LessByOrder(c.a, c.b); got != c.want {
				t.Errorf("LessByOrder = %v, want %v", got, c.want)
			}
		})
	}
}
