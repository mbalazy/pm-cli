package storage

import (
	"fmt"
	"strings"
	"testing"
)

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

// statusTask is orderTask's sibling for the rollup-shaping tests: what matters
// there is the status of parent and children, not their order.
func statusTask(id, parent string, status TaskStatus, brief string) *Task {
	return &Task{Meta: TaskMeta{ID: id, Parent: parent, Status: status, Brief: brief}, Project: "test"}
}

func TestBuildTrackersCompressesParentBrief(t *testing.T) {
	long := "**first line of the parent brief**\nsecond line\nthird line"
	tasks := []*Task{
		statusTask("p-3", "", StatusDoing, long),
		statusTask("p-3-1", "p-3", StatusTodo, "child line one\nchild line two"),
	}
	trackers, _ := BuildTrackers(tasks)
	if got := trackers[0].BriefLine; got != "first line of the parent brief" {
		t.Errorf("parent BriefLine = %q, want the first line stripped of bold markers", got)
	}
	if got := trackers[0].Children[0].BriefLine; got != "child line one" {
		t.Errorf("child BriefLine = %q, want the first line", got)
	}
}

func TestBuildTrackersParentBriefTruncatedAt120(t *testing.T) {
	tasks := []*Task{
		statusTask("p-4", "", StatusDoing, strings.Repeat("x", 500)),
		statusTask("p-4-1", "p-4", StatusTodo, ""),
	}
	trackers, _ := BuildTrackers(tasks)
	if got := len([]rune(trackers[0].BriefLine)); got != 121 {
		t.Errorf("parent BriefLine = %d runes, want 121 (120 + ellipsis)", got)
	}
}

func TestBuildTrackersCollapsesFinishedTracker(t *testing.T) {
	cases := []struct {
		name         string
		parent       TaskStatus
		kids         []TaskStatus
		wantCollapse bool
	}{
		{"closed parent, all children done", StatusDone, []TaskStatus{StatusDone, StatusDone}, true},
		{"closed parent, children merged", StatusDone, []TaskStatus{"merged", "merged"}, true},
		{"closed parent, mixed terminal", StatusDone, []TaskStatus{"merged", StatusDone, StatusArchived}, true},
		{"closed parent, one child still waiting", StatusDone, []TaskStatus{StatusDone, StatusWaiting}, false},
		{"closed parent, one child still todo", StatusDone, []TaskStatus{StatusDone, StatusTodo}, false},
		{"open parent, all children done", StatusDoing, []TaskStatus{StatusDone, StatusDone}, false},
		{"open parent, open children", StatusTodo, []TaskStatus{StatusTodo}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tasks := []*Task{statusTask("p-5", "", c.parent, "")}
			for i, ks := range c.kids {
				tasks = append(tasks, statusTask(fmt.Sprintf("p-5-%d", i+1), "p-5", ks, "kid brief"))
			}
			trackers, _ := BuildTrackers(tasks)
			tr := trackers[0]
			if tr.ChildrenOmitted != c.wantCollapse {
				t.Fatalf("ChildrenOmitted = %v, want %v", tr.ChildrenOmitted, c.wantCollapse)
			}
			if c.wantCollapse && len(tr.Children) != 0 {
				t.Errorf("collapsed tracker still lists %d children", len(tr.Children))
			}
			if !c.wantCollapse && len(tr.Children) != len(c.kids) {
				t.Errorf("children = %d, want %d", len(tr.Children), len(c.kids))
			}
			// Progress/Total survive the collapse - they are what a finished
			// tracker still has to say.
			if tr.Total != len(c.kids) {
				t.Errorf("Total = %d, want %d", tr.Total, len(c.kids))
			}
			sum := 0
			for _, n := range tr.Progress {
				sum += n
			}
			if sum != len(c.kids) {
				t.Errorf("progress sums to %d, want %d", sum, len(c.kids))
			}
		})
	}
}

func TestBuildTrackersCollapseDoesNotAffectSuppression(t *testing.T) {
	// A collapsed tracker's children are still suppressed from flat lists -
	// they are finished, not orphaned.
	tasks := []*Task{
		statusTask("p-6", "", StatusDone, ""),
		statusTask("p-6-1", "p-6", StatusDone, ""),
	}
	_, suppressed := BuildTrackers(tasks)
	if !suppressed["p-6"] || !suppressed["p-6-1"] {
		t.Errorf("suppressed = %v, want both parent and child", suppressed)
	}
}

// The whole point of extraTerminal: a project that renamed its landing statuses
// used to lose the collapse entirely, because "merged" was hardcoded here.
func TestBuildTrackersHonoursProjectLandingStatuses(t *testing.T) {
	tasks := []*Task{
		statusTask("p-9", "", StatusDone, ""),
		statusTask("p-9-1", "p-9", "pushed", "kid brief"),
		statusTask("p-9-2", "p-9", "review", "kid brief"),
	}

	t.Run("unknown landing statuses keep the tracker open", func(t *testing.T) {
		trackers, _ := BuildTrackers(tasks)
		if trackers[0].ChildrenOmitted {
			t.Error("without the project's landing statuses these children are not terminal - the tracker must stay expanded")
		}
	})

	t.Run("the project's own names collapse it", func(t *testing.T) {
		trackers, _ := BuildTrackers(tasks, TaskStatus("pushed"), TaskStatus("review"))
		if !trackers[0].ChildrenOmitted {
			t.Error("every child sits on a landing status - the finished tracker must collapse")
		}
		if trackers[0].Total != 2 {
			t.Errorf("Total = %d, want 2 (progress survives the collapse)", trackers[0].Total)
		}
	})
}
