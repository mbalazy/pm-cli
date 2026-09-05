package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func runTodayCmd(t *testing.T, store storage.TaskStore, args ...string) (string, error) {
	t.Helper()
	cmd := newTodayCmd(store)
	var out strings.Builder
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

func todayStore(t *testing.T) *storage.Store {
	t.Helper()
	store, slug := tempStore(t)
	if _, err := store.MutateProject(slug, func(p *storage.Project) error {
		p.Group = "grp"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, title string, st storage.TaskStatus, waitingFor, changed string) {
		task := &storage.Task{
			Meta:     storage.TaskMeta{ID: id, Title: title, Status: st, WaitingFor: waitingFor, StatusChanged: changed, Created: "2025-01-01", Updated: "2025-01-01T10:00:00+01:00"},
			FilePath: filepath.Join(store.ProjectDir(slug), id+".md"), Project: slug,
		}
		if err := storage.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	mk("proj-1", "Blocked on review", storage.StatusWaiting, "review", "2025-01-01T10:00:00+01:00")
	mk("proj-2", "Silent block", storage.StatusWaiting, "", "")
	return store
}

func TestTodayRendersSections(t *testing.T) {
	store := todayStore(t)
	out, err := runTodayCmd(t, store)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	for _, want := range []string{
		"# today  (wip",
		"## needs me (0)",
		"## waiting on (2)",
		"⧗ proj proj-1 Blocked on review · waiting for review · ",
		"⧗ proj proj-2 Silent block · waiting - no reason recorded · since ?  [no_reason]",
		"## changes (0)",
		"(no change feed yet)",
		"## stuck projects",
		"## groups",
		"grp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Row order: the stamped row first, the unknown-age row last.
	if strings.Index(out, "proj-1") > strings.Index(out, "proj-2") {
		t.Errorf("unknown age must sort last:\n%s", out)
	}
}

func TestTodayJSONIsTheAPIShape(t *testing.T) {
	store := todayStore(t)
	out, err := runTodayCmd(t, store, "--json", "--group", "grp")
	if err != nil {
		t.Fatalf("today --json: %v", err)
	}
	var a storage.Attention
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("not the Attention shape: %v\n%s", err, out)
	}
	if sec := a.Section(storage.SectionWaiting); sec == nil || sec.Total != 2 {
		t.Fatalf("waiting = %+v", sec)
	}
	if len(a.Groups) != 1 || a.Groups[0].Slug != "grp" || a.Groups[0].Waiting != 2 {
		t.Fatalf("groups = %+v", a.Groups)
	}
	if _, err := runTodayCmd(t, store, "--project", "nosuch"); err == nil {
		t.Fatal("unknown project must error")
	}
}
