package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func TestResolveWorkerTimeoutPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		flagValue  time.Duration
		flagSet    bool
		configured storage.Duration
		want       time.Duration
	}{
		{"nothing set anywhere", defaultWorkerTimeout, false, 0, defaultWorkerTimeout},
		{"project value, no flag", defaultWorkerTimeout, false, storage.Duration(90 * time.Minute), 90 * time.Minute},
		{"explicit flag beats the project", 20 * time.Minute, true, storage.Duration(90 * time.Minute), 20 * time.Minute},
		// The flag can also be used to LOWER a project's generous ceiling, which
		// only works because "passed" and "left at the default" are distinguished.
		{"explicit flag equal to the default still beats the project", defaultWorkerTimeout, true, storage.Duration(3 * time.Hour), defaultWorkerTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveWorkerTimeout(tt.flagValue, tt.flagSet, tt.configured); got != tt.want {
				t.Fatalf("resolveWorkerTimeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDescribeTimeoutNamesTheSource(t *testing.T) {
	tests := []struct {
		flagSet    bool
		configured storage.Duration
		want       string
	}{
		{true, storage.Duration(90 * time.Minute), "--timeout"},
		{false, storage.Duration(90 * time.Minute), "executor.timeout"},
		{false, 0, "built-in default"},
	}
	for _, tt := range tests {
		got := describeTimeout(90*time.Minute, tt.flagSet, tt.configured)
		if !strings.Contains(got, tt.want) {
			t.Errorf("describeTimeout(flagSet=%v, configured=%s) = %q, want it to name %q", tt.flagSet, tt.configured, got, tt.want)
		}
	}
}

// TestPlanWorkHonorsProjectTimeout: the value has to reach the PLAN, because
// that is what executeWork hands to the worker's context deadline. A project
// field nobody plumbs through is the same as no field.
func TestPlanWorkHonorsProjectTimeout(t *testing.T) {
	store, _ := epicFixture(t, &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "merged",
		Timeout: storage.Duration(100 * time.Minute),
	})
	task, err := store.FindTask("app", "app-1-1")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := planWork(store, task, "app", workOptions{standalone: true, timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planWork: %v", err)
	}
	if plan.timeout != 100*time.Minute {
		t.Fatalf("plan.timeout = %s, want the project's 100m", plan.timeout)
	}

	plan, err = planWork(store, task, "app", workOptions{standalone: true, timeout: 5 * time.Minute, timeoutSet: true})
	if err != nil {
		t.Fatalf("planWork with --timeout: %v", err)
	}
	if plan.timeout != 5*time.Minute {
		t.Fatalf("plan.timeout = %s, want the explicit --timeout 5m", plan.timeout)
	}
}

// TestPlanEpicHonorsProjectTimeout: the manager resolves it ONCE for the run and
// every sub inherits that number (executeEpic passes it as an already-set
// timeout), so a project value cannot apply to some subs and not others.
func TestPlanEpicHonorsProjectTimeout(t *testing.T) {
	store, _ := epicFixture(t, &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "merged",
		Timeout: storage.Duration(100 * time.Minute),
	})

	plan, err := planEpic(store, []string{"app", "app-1"}, epicOptions{timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planEpic: %v", err)
	}
	if plan.timeout != 100*time.Minute {
		t.Fatalf("plan.timeout = %s, want the project's 100m", plan.timeout)
	}

	plan, err = planEpic(store, []string{"app", "app-1"}, epicOptions{timeout: 7 * time.Minute, timeoutSet: true})
	if err != nil {
		t.Fatalf("planEpic with --timeout: %v", err)
	}
	if plan.timeout != 7*time.Minute {
		t.Fatalf("plan.timeout = %s, want the explicit --timeout 7m", plan.timeout)
	}
}

// TestExecutorShowReportsTimeout: `pm executor show` is where a human checks
// what a run will actually get, so an unset field has to report the built-in
// number rather than nothing at all.
func TestExecutorShowReportsTimeout(t *testing.T) {
	configured := renderExecutorProfile("app", &storage.Project{
		Name: "App", Path: t.TempDir(),
		Executor: &storage.Executor{Enabled: true, Timeout: storage.Duration(90 * time.Minute)},
	})
	if !strings.Contains(configured, "timeout:      1h30m0s per worker") {
		t.Errorf("configured timeout not shown:\n%s", configured)
	}

	unset := renderExecutorProfile("app", &storage.Project{
		Name: "App", Path: t.TempDir(),
		Executor: &storage.Executor{Enabled: true},
	})
	if !strings.Contains(unset, "2h0m0s per worker (built-in default") {
		t.Errorf("unset timeout must still report the effective default:\n%s", unset)
	}
}
