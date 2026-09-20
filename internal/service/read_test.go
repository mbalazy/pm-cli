package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// seedProject writes a second project "beta" (statuses todo/doing/merged/done,
// one declared journal "sim", an executor block) holding a tracker with two
// children, a doing task with an oversized body and n todo tasks.
func seedProject(t *testing.T, store *storage.Store, todos int) {
	t.Helper()
	projDir := filepath.Join(store.Root, "beta")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Beta\nprefix: b\npath: /tmp/beta\nstatuses: [todo, doing, merged, done]\njournals:\n  - name: sim\n    subject: the simulator\nexecutor:\n  baseline: go test ./...\n"
	if err := os.WriteFile(filepath.Join(projDir, "project.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	mk := func(id, title, parent string, st storage.TaskStatus, brief, body string) {
		task := &storage.Task{
			Meta:     storage.TaskMeta{ID: id, Title: title, Status: st, Parent: parent, Created: "2025-02-02", Updated: "2025-02-02T10:00:00+02:00", Brief: brief},
			Body:     body,
			FilePath: filepath.Join(projDir, id+".md"),
			Project:  "beta",
		}
		if err := storage.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	mk("b-1", "Tracker", "", storage.StatusDoing, "line1\nline2", "spec")
	mk("b-1-1", "Child one", "b-1", storage.StatusDone, "", "")
	mk("b-1-2", "Child two", "b-1", storage.StatusDoing, "child brief", "")
	mk("b-2", "Doing standalone", "", storage.StatusDoing, "brief a\nbrief b", strings.Repeat("x", ContextBodyLimit+500))
	mk("b-3", "Archived one", "", storage.StatusArchived, "", "")
	for i := 0; i < todos; i++ {
		id := "b-t" + strings.Repeat("0", 3-len(itoa(i))) + itoa(i)
		mk(id, "Todo "+itoa(i), "", storage.StatusTodo, "", "")
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestListTasks(t *testing.T) {
	t.Run("default budget, newest first, one-line briefs, archived excluded", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 60)
		res, err := ListTasks(store, ListTasksInput{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Shown != DefaultListLimit || res.Total != 65 || !strings.Contains(res.Note, "raise limit") {
			t.Fatalf("shown %d total %d note %q", res.Shown, res.Total, res.Note)
		}
		for _, s := range res.Tasks {
			if s.Status == "archived" {
				t.Fatalf("archived task listed: %s", s.ID)
			}
			if strings.Contains(s.Brief, "\n") {
				t.Fatalf("brief not one line: %q", s.Brief)
			}
		}
		for i := 1; i < len(res.Tasks); i++ {
			if res.Tasks[i-1].Updated < res.Tasks[i].Updated {
				t.Fatalf("not newest first at %d", i)
			}
		}
	})

	t.Run("limit is capped and the note says so", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 0)
		res, err := ListTasks(store, ListTasksInput{Limit: 999})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Note, "limit capped at 200") {
			t.Fatalf("note = %q", res.Note)
		}
		if res.Shown != res.Total {
			t.Fatalf("shown %d total %d", res.Shown, res.Total)
		}
	})

	t.Run("status=archived lists only archived; an unknown status is a ValidationError", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 0)
		res, err := ListTasks(store, ListTasksInput{Project: "beta", Status: "archived"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 || res.Tasks[0].ID != "b-3" {
			t.Fatalf("archived listing: %+v", res)
		}
		_, err = ListTasks(store, ListTasksInput{Project: "beta", Status: "shipped"})
		wantValidation(t, err)
		// cross-project validates against the union: merged is beta's only
		if _, err := ListTasks(store, ListTasksInput{Status: "merged"}); err != nil {
			t.Fatalf("union status rejected: %v", err)
		}
		_, err = ListTasks(store, ListTasksInput{Project: "test", Status: "merged"})
		wantValidation(t, err)
	})

	t.Run("empty result is an empty array, not null", func(t *testing.T) {
		store := newTestStore(t)
		res, err := ListTasks(store, ListTasksInput{Project: "test", Status: "done"})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		if !strings.Contains(string(b), `"tasks":[]`) {
			t.Fatalf("json = %s", b)
		}
	})
}

func TestGetTask(t *testing.T) {
	store := newTestStore(t)
	d, err := GetTask(store, GetTaskInput{Project: "test", TaskID: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "t-1" || d.Body == "" || d.Brief != "initial brief" {
		t.Fatalf("detail %+v", d)
	}
	if _, err := GetTask(store, GetTaskInput{Project: "test", TaskID: "nope"}); err == nil {
		t.Fatal("expected not found")
	}
}

func TestContext(t *testing.T) {
	t.Run("project-scoped: full briefs, capped bodies, trackers, pointers, counts", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 2)
		if err := storage.WriteFocusPlan(store.RootDir(), storage.FocusPlan{Date: storage.Today(), Tasks: []string{"b-2", "t-1"}}); err != nil {
			t.Fatal(err)
		}
		out, err := Context(store, ContextInput{Project: "beta"})
		if err != nil {
			t.Fatal(err)
		}
		pc, ok := out.(*ProjectContextResult)
		if !ok {
			t.Fatalf("got %T", out)
		}
		if pc.Project.Slug != "beta" || strings.Join(pc.Project.Statuses, ",") != "todo,doing,merged,done" {
			t.Fatalf("project meta %+v", pc.Project)
		}
		// trackers and their children are suppressed from doing_tasks
		if len(pc.DoingTasks) != 1 || pc.DoingTasks[0].ID != "b-2" {
			t.Fatalf("doing_tasks = %+v", pc.DoingTasks)
		}
		d := pc.DoingTasks[0]
		if d.Brief != "brief a\nbrief b" {
			t.Fatalf("project-scoped brief must stay full: %q", d.Brief)
		}
		if len([]rune(d.Body)) > ContextBodyLimit+100 || !strings.HasSuffix(d.Body, "[body truncated - use pm_get_task for the full body]") {
			t.Fatalf("body not capped: len %d", len(d.Body))
		}
		if len(pc.Trackers) != 1 || pc.Trackers[0].ID != "b-1" {
			t.Fatalf("trackers = %+v", pc.Trackers)
		}
		if pc.TaskCounts["todo"] != 2 || pc.TaskCounts["doing"] != 3 || pc.TaskCounts["archived"] != 1 {
			t.Fatalf("counts = %v", pc.TaskCounts)
		}
		if !strings.Contains(pc.ExecutorProfile, "pm executor show beta") {
			t.Fatalf("executor pointer = %q", pc.ExecutorProfile)
		}
		if len(pc.Journals) != 1 || pc.Journals[0].Name != "sim" || pc.JournalsNote == "" {
			t.Fatalf("journals = %+v", pc.Journals)
		}
		if len(pc.FocusTasks) != 2 {
			t.Fatalf("focus = %+v", pc.FocusTasks)
		}
		// key set on the wire
		b, _ := json.Marshal(pc)
		for _, k := range []string{`"doing_tasks"`, `"executor_profile"`, `"focus_tasks"`, `"journals"`, `"journals_note"`, `"project"`, `"task_counts"`, `"trackers"`} {
			if !strings.Contains(string(b), k) {
				t.Fatalf("missing %s in %s", k, b)
			}
		}
	})

	t.Run("project-scoped: optional keys are absent when empty", func(t *testing.T) {
		store := newTestStore(t)
		out, err := Context(store, ContextInput{Project: "test"})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		for _, k := range []string{`"trackers"`, `"executor_profile"`, `"journals"`, `"focus_tasks"`} {
			if strings.Contains(string(b), k) {
				t.Fatalf("unexpected %s in %s", k, b)
			}
		}
	})

	t.Run("cross-project: one-line briefs, cwd miss note, warnings", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 0)
		out, err := Context(store, ContextInput{Cwd: "/nowhere"})
		if err != nil {
			t.Fatal(err)
		}
		cc, ok := out.(*CrossProjectContextResult)
		if !ok {
			t.Fatalf("got %T", out)
		}
		if !strings.Contains(cc.Note, `cwd "/nowhere" matches no configured project`) {
			t.Fatalf("note = %q", cc.Note)
		}
		if len(cc.Projects) != 2 {
			t.Fatalf("projects = %+v", cc.Projects)
		}
		for _, p := range cc.Projects {
			for _, d := range p.DoingTasks {
				if strings.Contains(d.Brief, "\n") {
					t.Fatalf("cross-project brief must be one line: %q", d.Brief)
				}
			}
		}
		// cwd under a configured path resolves to that project
		out, err = Context(store, ContextInput{Cwd: "/tmp/beta/sub"})
		if err != nil {
			t.Fatal(err)
		}
		if pc, ok := out.(*ProjectContextResult); !ok || pc.Project.Slug != "beta" {
			t.Fatalf("cwd resolve: %T %+v", out, out)
		}
	})

	t.Run("unknown project errors", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := Context(store, ContextInput{Project: "zzz"}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestProjects(t *testing.T) {
	t.Run("list counts per status and flags archived", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 1)
		res, err := ListProjects(store)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Projects) != 2 || res.Note != "" {
			t.Fatalf("%+v", res)
		}
		// No group set: the project is its own group, named by its slug.
		for _, p := range res.Projects {
			if p.Group != p.Slug || p.GroupName != p.Slug {
				t.Errorf("ungrouped %s: group=%q group_name=%q", p.Slug, p.Group, p.GroupName)
			}
		}
	})

	t.Run("list carries the group and its configured name", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 1)
		if _, err := store.MutateProject("beta", func(p *storage.Project) error { p.Group = "acme"; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  groups:\n    acme: {name: Acme}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := ListProjects(store)
		if err != nil {
			t.Fatal(err)
		}
		byslug := map[string]ProjectInfo{}
		for _, p := range res.Projects {
			byslug[p.Slug] = p
		}
		if b := byslug["beta"]; b.Group != "acme" || b.GroupName != "Acme" {
			t.Errorf("beta = %+v", b)
		}
		if x := byslug["test"]; x.Group != "test" || x.GroupName != "test" {
			t.Errorf("test = %+v", x)
		}

		// A broken config is named in the note; names fall back to slugs.
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  cutoff_hour: 99\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err = ListProjects(store)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Note, "cutoff_hour") {
			t.Errorf("note = %q, want the config error", res.Note)
		}
		for _, p := range res.Projects {
			if p.Slug == "beta" && (p.Group != "acme" || p.GroupName != "acme") {
				t.Errorf("fallback beta = %+v", p)
			}
		}
	})

	t.Run("groups lists active projects by group, loud on a broken config", func(t *testing.T) {
		store := newTestStore(t)
		seedProject(t, store, 1)
		if _, err := store.MutateProject("beta", func(p *storage.Project) error { p.Group = "acme"; return nil }); err != nil {
			t.Fatal(err)
		}
		res, err := ListGroups(store)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Groups) != 2 || res.Groups[0].Slug != "acme" || res.Groups[0].Projects[0] != "beta" || res.Groups[1].Slug != "test" {
			t.Fatalf("%+v", res.Groups)
		}
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  cutoff_hour: 99\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ListGroups(store); err == nil {
			t.Fatal("ListGroups must fail on a broken config")
		}
	})

	t.Run("create and update set the group, an unsafe one is a validation error", func(t *testing.T) {
		store := newTestStore(t)
		res, err := CreateProject(store, CreateProjectInput{Slug: "acme-api", Group: "acme"})
		if err != nil || res.Group != "acme" {
			t.Fatalf("%+v, %v", res, err)
		}
		_, err = CreateProject(store, CreateProjectInput{Slug: "bad", Group: "Not A Slug"})
		wantValidation(t, err)
		if _, err := store.GetProject("bad"); err == nil {
			t.Fatal("rejected create wrote a project")
		}

		bad := "Not A Slug"
		_, err = UpdateProject(store, UpdateProjectInput{Project: "acme-api", Group: &bad})
		wantValidation(t, err)
		proj, _ := store.GetProject("acme-api")
		if proj.Group != "acme" {
			t.Fatalf("rejected group was written: %q", proj.Group)
		}
		// nil keeps, "" clears.
		if res, err := UpdateProject(store, UpdateProjectInput{Project: "acme-api", Name: "AcmeApi"}); err != nil || res.Group != "acme" {
			t.Fatalf("omit must keep: %+v, %v", res, err)
		}
		empty := ""
		if res, err := UpdateProject(store, UpdateProjectInput{Project: "acme-api", Group: &empty}); err != nil || res.Group != "" {
			t.Fatalf("empty must clear: %+v, %v", res, err)
		}
	})

	t.Run("create validates the slug and refuses case-insensitive duplicates", func(t *testing.T) {
		store := newTestStore(t)
		res, err := CreateProject(store, CreateProjectInput{Slug: "gamma", Prefix: "g", Statuses: []string{"todo", "done"}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Slug != "gamma" || res.Name != "gamma" || len(res.Statuses) != 2 {
			t.Fatalf("%+v", res)
		}
		for _, slug := range []string{"", "../x", "Gamma", "TEST"} {
			_, err := CreateProject(store, CreateProjectInput{Slug: slug})
			wantValidation(t, err)
		}
	})

	t.Run("update patches given fields, merges links, refuses an unsafe prefix", func(t *testing.T) {
		store := newTestStore(t)
		yes := true
		res, err := UpdateProject(store, UpdateProjectInput{Project: "test", Name: "Renamed", Links: map[string]string{"a": "b"}, Archived: &yes})
		if err != nil {
			t.Fatal(err)
		}
		if res.Name != "Renamed" || res.Prefix != "t" || res.Links["a"] != "b" || !res.Archived {
			t.Fatalf("%+v", res)
		}
		_, err = UpdateProject(store, UpdateProjectInput{Project: "test", Prefix: "../x"})
		wantValidation(t, err)
		proj, _ := store.GetProject("test")
		if proj.Prefix != "t" {
			t.Fatalf("rejected prefix was written: %q", proj.Prefix)
		}
	})
}

func TestValidateStatusesKeepTasks(t *testing.T) {
	store := newTestStore(t) // t-1 sits on doing
	cases := []struct {
		name     string
		statuses []string
		wantErr  string
	}{
		{"keeps the occupied status", []string{"todo", "doing"}, ""},
		{"case-insensitive", []string{"TODO", "Doing"}, ""},
		{"drops the occupied status", []string{"todo", "done"}, "new statuses would orphan tasks on: doing (1 task(s))"},
		{"empty list resets to defaults, which hold doing", []string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateStatusesKeepTasks(store, "test", c.statuses)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			wantValidation(t, err)
			if err.Error() != c.wantErr {
				t.Fatalf("error = %q, want %q", err, c.wantErr)
			}
		})
	}
	t.Run("archived tasks never count", func(t *testing.T) {
		store := newTestStore(t)
		task, _ := store.FindTaskExact("test", "t-1")
		if err := store.MoveTask(task, storage.StatusArchived); err != nil {
			t.Fatal(err)
		}
		if err := validateStatusesKeepTasks(store, "test", []string{"todo"}); err != nil {
			t.Fatalf("archived task blocked the update: %v", err)
		}
	})
	t.Run("through UpdateProject nothing is written on refusal", func(t *testing.T) {
		store := newTestStore(t)
		_, err := UpdateProject(store, UpdateProjectInput{Project: "test", Statuses: []string{"todo", "done"}})
		wantValidation(t, err)
		proj, _ := store.GetProject("test")
		if len(proj.Statuses) != 0 {
			t.Fatalf("statuses written despite refusal: %v", proj.Statuses)
		}
	})
}

func TestJournal(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store, 0)

	t.Run("list without a name declares; undeclared project says so", func(t *testing.T) {
		out, err := JournalList(store, JournalListInput{Project: "beta"})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Declared) != 1 || out.Declared[0].Name != "sim" || out.Entries == nil {
			t.Fatalf("%+v", out)
		}
		out, err = JournalList(store, JournalListInput{Project: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Note, "no journals declared") {
			t.Fatalf("note = %q", out.Note)
		}
	})

	t.Run("undeclared name, empty name and a bad date are ValidationErrors", func(t *testing.T) {
		_, err := JournalList(store, JournalListInput{Project: "beta", Name: "nope"})
		wantValidation(t, err)
		_, err = JournalAdd(store, JournalAddInput{Project: "beta", Name: "", Symptom: "s"})
		wantValidation(t, err)
		_, err = JournalAdd(store, JournalAddInput{Project: "beta", Name: "sim", Symptom: "s", Date: "yesterday"})
		wantValidation(t, err)
	})

	t.Run("add reports recurrence; list applies open filter and budget", func(t *testing.T) {
		if _, err := JournalAdd(store, JournalAddInput{Project: "beta", Name: "sim", Symptom: "s1", Date: "2025-03-01", Tags: []string{"boot"}}); err != nil {
			t.Fatal(err)
		}
		res, err := JournalAdd(store, JournalAddInput{Project: "beta", Name: "sim", Symptom: "s2", Date: "2025-03-02", Tags: []string{"boot"}, Fix: "fixed"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 2 || res.Open != 1 || !strings.Contains(res.Note, `2 entries now share tag "boot"`) {
			t.Fatalf("%+v", res)
		}
		out, err := JournalList(store, JournalListInput{Project: "beta", Name: "sim", Open: true})
		if err != nil {
			t.Fatal(err)
		}
		if out.Shown != 1 || out.Entries[0].Symptom != "s1" || out.Total != 2 || out.Open != 1 {
			t.Fatalf("%+v", out)
		}
		out, err = JournalList(store, JournalListInput{Project: "beta", Name: "sim", Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Note, "limit capped at 200") || out.Entries[0].Symptom != "s2" {
			t.Fatalf("newest first + cap note: %+v", out)
		}
	})
}

// attentionFixture adds to the two-project store a waiting task without a
// reason, an old waiting task, and a failed run on a tracker, so every
// digest field has something to carry.
func attentionFixture(t *testing.T, store *storage.Store) {
	t.Helper()
	seedProject(t, store, 1)
	// The fixture is about the executor's rows (off by default since pm-cli-125).
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  show_executor: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateProject("beta", func(p *storage.Project) error {
		p.Statuses = []string{"todo", "doing", "waiting", "merged", "done"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(slug, id, title string, st storage.TaskStatus, parent, waitingFor, changed string) {
		task := &storage.Task{
			Meta:     storage.TaskMeta{ID: id, Title: title, Status: st, Parent: parent, WaitingFor: waitingFor, StatusChanged: changed, Created: "2025-02-02", Updated: "2025-02-02T10:00:00+02:00"},
			FilePath: filepath.Join(store.ProjectDir(slug), id+".md"),
			Project:  slug,
		}
		if err := storage.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	mk("beta", "b-w1", "no reason", storage.StatusWaiting, "", "", "2025-02-01T10:00:00+02:00")
	mk("beta", "b-w2", "old wait", storage.StatusWaiting, "", "review", "2024-12-01T10:00:00+02:00")
	mk("beta", "b-e", "epic", storage.StatusDoing, "", "", "")
	mk("beta", "b-e-1", "sub", storage.StatusTodo, "b-e", "", "")
	if err := storage.WriteRunState(store.ProjectDir("beta"), &storage.RunState{TaskID: "b-e", Project: "beta", Kind: storage.RunKindEpic, Status: storage.RunStatusFailed, PID: 1, Started: "2025-02-03T10:00:00+02:00", Updated: "2025-02-03T11:00:00+02:00", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
}

func TestAttention(t *testing.T) {
	t.Run("scopes and digests", func(t *testing.T) {
		store := newTestStore(t)
		attentionFixture(t, store)
		a, err := Attention(store, AttentionInput{})
		if err != nil {
			t.Fatal(err)
		}
		if a.Section(storage.SectionNeedsMe).Total != 1 || a.Section(storage.SectionWaiting).Total != 2 {
			t.Fatalf("%+v", a.Sections)
		}
		scoped, err := Attention(store, AttentionInput{Project: "t"}) // prefix resolves
		if err != nil {
			t.Fatal(err)
		}
		if scoped.Section(storage.SectionNeedsMe).Total != 0 || scoped.Section(storage.SectionWaiting).Total != 0 {
			t.Fatalf("project scope leaked beta rows: %+v", scoped.Sections)
		}
		if _, err := Attention(store, AttentionInput{Project: "nosuch"}); err == nil {
			t.Fatal("unknown project must error")
		}

		d := DigestAttention(a)
		if d.WIP != a.WIP || len(d.NeedsMe) != 1 || d.NeedsMe[0].TaskID != "b-e" || !strings.Contains(d.NeedsMe[0].Reason, "boom") {
			t.Fatalf("digest needs_me = %+v", d.NeedsMe)
		}
		// no_reason first, although b-w2 is older.
		if d.WaitingTotal != 2 || len(d.Waiting) != 2 || d.Waiting[0].TaskID != "b-w1" || d.Waiting[1].TaskID != "b-w2" {
			t.Fatalf("digest waiting = %+v", d.Waiting)
		}
		for _, name := range []string{storage.SectionFocus, storage.SectionInProgress, storage.SectionChanges, storage.SectionLandedNoPR} {
			if _, ok := d.Counts[name]; !ok {
				t.Errorf("counts lacks %s: %v", name, d.Counts)
			}
		}
		if _, ok := d.Counts[storage.SectionWaiting]; ok {
			t.Error("waiting is a list in the digest, not a count")
		}
		if DigestAttention(nil) != nil {
			t.Error("nil in, nil out")
		}
	})

	t.Run("digest caps waiting at the limit", func(t *testing.T) {
		rows := make([]storage.AttentionRow, 0, ContextWaitingLimit+5)
		for i := 0; i < ContextWaitingLimit+5; i++ {
			rows = append(rows, storage.AttentionRow{Section: storage.SectionWaiting, TaskID: fmt.Sprintf("w-%d", i)})
		}
		rows[ContextWaitingLimit+3].Flags = []string{storage.FlagNoReason}
		d := DigestAttention(&storage.Attention{Sections: []storage.AttentionSection{{Name: storage.SectionWaiting, Rows: rows, Total: len(rows)}}})
		if len(d.Waiting) != ContextWaitingLimit || d.WaitingTotal != ContextWaitingLimit+5 {
			t.Fatalf("len=%d total=%d", len(d.Waiting), d.WaitingTotal)
		}
		if d.Waiting[0].TaskID != fmt.Sprintf("w-%d", ContextWaitingLimit+3) {
			t.Errorf("the no_reason row past the cap must be promoted, got %s first", d.Waiting[0].TaskID)
		}
	})

	t.Run("context carries the block, and a broken config becomes a note", func(t *testing.T) {
		store := newTestStore(t)
		attentionFixture(t, store)
		cross, err := CrossProjectContext(store, "")
		if err != nil {
			t.Fatal(err)
		}
		if cross.Attention == nil || len(cross.Attention.NeedsMe) != 1 {
			t.Fatalf("cross attention = %+v", cross.Attention)
		}
		proj, err := ProjectContext(store, "beta")
		if err != nil {
			t.Fatal(err)
		}
		if proj.Attention == nil || proj.Attention.WaitingTotal != 2 || proj.AttentionNote != "" {
			t.Fatalf("project attention = %+v note=%q", proj.Attention, proj.AttentionNote)
		}
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  cutoff_hour: 99\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cross, err = CrossProjectContext(store, "")
		if err != nil {
			t.Fatal(err)
		}
		if cross.Attention != nil || !strings.Contains(cross.Note, "cutoff_hour") {
			t.Fatalf("broken config: attention=%+v note=%q", cross.Attention, cross.Note)
		}
		proj, _ = ProjectContext(store, "beta")
		if proj.Attention != nil || !strings.Contains(proj.AttentionNote, "cutoff_hour") {
			t.Fatalf("broken config (project): %+v %q", proj.Attention, proj.AttentionNote)
		}
	})
}
