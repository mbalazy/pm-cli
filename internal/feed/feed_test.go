package feed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

var (
	feedNow  = time.Date(2026, 9, 9, 15, 0, 0, 0, time.Local)
	feedFrom = time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local)
)

func at(d time.Duration) string { return feedNow.Add(-d).Format(time.RFC3339) }

// fakeSource is a Source a test scripts.
type fakeSource struct {
	name   string
	events []Event
	err    error
	slow   time.Duration
	calls  int
}

func (s *fakeSource) Name() string { return s.name }
func (s *fakeSource) Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error) {
	s.calls++
	if s.slow > 0 {
		select {
		case <-time.After(s.slow):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.events, s.err
}

func feedStore(t *testing.T) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("alpha", &storage.Project{Name: "alpha", Group: "grp", Statuses: []string{"todo", "doing", "waiting", "merged", "done"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProject("asleep", &storage.Project{Name: "asleep", Archived: true}); err != nil {
		t.Fatal(err)
	}
	return store
}

func writeTask(t *testing.T, store *storage.Store, slug string, meta storage.TaskMeta) {
	t.Helper()
	task := &storage.Task{Meta: meta, Project: slug, FilePath: filepath.Join(store.ProjectDir(slug), meta.ID+".md")}
	if err := store.WriteTask(task); err != nil {
		t.Fatal(err)
	}
}

func cfgWith(sources map[string]bool) *storage.CockpitConfig {
	c := storage.DefaultCockpitConfig()
	for k, v := range sources {
		c.Sources[k] = v
	}
	return &c
}

func TestRefreshIsolatesSources(t *testing.T) {
	store := feedStore(t)
	ok := &fakeSource{name: "pm", events: []Event{{ID: "e1", TS: at(time.Hour), Project: "alpha", Title: "one"}}}
	bad := &fakeSource{name: "git", err: errors.New("gh: not logged in")}
	off := &fakeSource{name: "slack", events: []Event{{ID: "e9", TS: at(time.Minute), Title: "never"}}}
	f := New(store.Root, []Source{ok, bad, off})

	res, err := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || off.calls != 0 {
		t.Fatalf("added=%d off.calls=%d", res.Added, off.calls)
	}
	byName := map[string]SourceStatus{}
	for _, s := range res.Sources {
		byName[s.Name] = s
	}
	if s := byName["pm"]; !s.Enabled || s.Error != "" || s.Events != 1 || s.LastFetch == "" {
		t.Errorf("pm = %+v", s)
	}
	if s := byName["git"]; !s.Enabled || !strings.Contains(s.Error, "not logged in") {
		t.Errorf("git = %+v", s)
	}
	if s := byName["slack"]; s.Enabled || s.LastFetch != "" {
		t.Errorf("slack = %+v", s)
	}
	// Order follows the registry, not the map.
	if res.Sources[0].Name != "pm" || res.Sources[1].Name != "git" || res.Sources[2].Name != "slack" {
		t.Errorf("order = %+v", res.Sources)
	}

	ch, err := f.Read(feedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Events) != 1 || ch.Events[0].Source != "pm" || ch.Unseen != 1 || ch.Events[0].Seen {
		t.Fatalf("read = %+v", ch)
	}
}

func TestRefreshTimeoutIsASourceError(t *testing.T) {
	store := feedStore(t)
	slow := &fakeSource{name: "pm", slow: time.Second}
	f := New(store.Root, []Source{slow})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res, err := f.Refresh(ctx, store, cfgWith(nil), feedFrom, feedNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sources[0].Error == "" || !strings.Contains(res.Sources[0].Error, "deadline") {
		t.Fatalf("timeout must land in the source status: %+v", res.Sources[0])
	}
}

func TestDedupSeenAndRotation(t *testing.T) {
	store := feedStore(t)
	src := &fakeSource{name: "pm", events: []Event{
		{ID: "a", TS: at(3 * time.Hour), Title: "a"},
		{ID: "b", TS: at(2 * time.Hour), Title: "b"},
		{ID: "old", TS: feedNow.AddDate(0, 0, -20).Format(time.RFC3339), Title: "old"},
	}}
	f := New(store.Root, []Source{src})
	// The 20-day-old event is outside the 14-day keep window: never written.
	if res, _ := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom.AddDate(0, 0, -30), feedNow); res.Added != 2 {
		t.Fatalf("first added = %d", res.Added)
	}
	// Same ids again plus one new: only the new one is added.
	src.events = append(src.events, Event{ID: "c", TS: at(time.Hour), Title: "c"})
	if res, _ := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow); res.Added != 1 {
		t.Fatalf("second added = %d", res.Added)
	}
	// A day file that aged past the keep window is rotated out.
	stale := filepath.Join(CacheDir(store.Root), feedNow.AddDate(0, 0, -20).Format("2006-01-02")+".jsonl")
	if err := os.WriteFile(stale, []byte(`{"id":"stale","ts":"2026-08-20T10:00:00+02:00"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("old day file survived rotation: %v", err)
	}

	ch, _ := f.Read(feedFrom)
	if got := eventIDs(ch.Events); got != "c,b,a" {
		t.Fatalf("newest first: %s", got)
	}
	if err := f.MarkSeen(feedNow.Add(-90 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	ch, _ = f.Read(feedFrom)
	if ch.Unseen != 1 || !ch.Events[1].Seen || !ch.Events[2].Seen || ch.Events[0].Seen {
		t.Fatalf("seen flags = %+v", ch.Events)
	}
	d, err := f.Digest(feedFrom, 5)
	if err != nil || d == nil || d.Count != 1 || len(d.Rows) != 1 || d.Rows[0].Title != "c" || d.Rows[0].Section != storage.SectionChanges {
		t.Fatalf("digest = %+v, %v", d, err)
	}
}

func TestDigestWithoutAFeedIsNil(t *testing.T) {
	f := New(t.TempDir(), []Source{})
	d, err := f.Digest(feedFrom, 5)
	if err != nil || d != nil {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func eventIDs(events []Event) string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	return strings.Join(ids, ",")
}

func TestPMSource(t *testing.T) {
	// Real time here: WriteRunState stamps Updated itself, so the window
	// has to be around now.
	now := time.Now()
	from, to := now.Add(-24*time.Hour), now.Add(time.Hour)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	store := feedStore(t)
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-1", Title: "moved", Status: storage.StatusWaiting, Created: "2026-09-01", Updated: at(time.Hour), StatusChanged: at(2 * time.Hour)})
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-2", Title: "fresh", Status: storage.StatusTodo, Created: now.Format("2006-01-02"), Updated: at(time.Hour)})
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-3", Title: "old move", Status: storage.StatusDone, Created: "2026-09-01", Updated: at(time.Hour), StatusChanged: at(48 * time.Hour)})
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-4", Title: "epic", Status: storage.StatusDoing, Created: "2026-09-01", Updated: at(time.Hour)})
	writeTask(t, store, "asleep", storage.TaskMeta{ID: "z-1", Title: "asleep move", Status: storage.StatusDoing, Created: "2026-09-01", Updated: at(time.Hour), StatusChanged: at(time.Hour)})
	dir := store.ProjectDir("alpha")
	if err := storage.AppendJournal(dir, &storage.JournalEntry{Event: "end", TS: at(30 * time.Minute), Kind: "run-epic", Project: "alpha", TaskID: "a-4", Status: "failed", Subs: []storage.JournalSub{{ID: "a-4-1", Result: "merged"}, {ID: "a-4-2", Result: "failed"}}}); err != nil {
		t.Fatal(err)
	}
	if err := storage.AppendJournal(dir, &storage.JournalEntry{Event: "start", TS: at(72 * time.Hour), Kind: "run-epic", Project: "alpha", TaskID: "a-4"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunState(dir, &storage.RunState{TaskID: "a-4", Project: "alpha", Kind: storage.RunKindFinish, Status: storage.RunStatusDone, PID: 1, Started: at(20 * time.Minute), Updated: at(10 * time.Minute), Subs: []storage.SubRun{{ID: "a-4", Status: "partial", VisualClaimsOpen: 3}}}); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteFocusPlan(store.Root, storage.FocusPlan{Date: now.Format("2006-01-02"), Tasks: []string{"a-1"}}); err != nil {
		t.Fatal(err)
	}

	f := New(store.Root, []Source{&PMSource{Root: store.Root}})
	if _, err := f.Refresh(context.Background(), store, cfgWith(nil), from, to); err != nil {
		t.Fatal(err)
	}
	ch, _ := f.Read(from)
	details := map[string]Event{}
	for _, e := range ch.Events {
		details[e.TaskID+"|"+e.Detail] = e
	}
	for _, want := range []string{
		"a-1|moved to waiting",
		"a-2|created",
		"a-4|run-epic ended: failed (1 merged, 1 failed)",
		"a-4|acceptance partial, 3 visual claim(s) open",
	} {
		if _, ok := details[want]; !ok {
			t.Errorf("missing %q in %v", want, keysOf(details))
		}
	}
	if e := details["a-1|moved to waiting"]; e.Severity != SeverityWarn || e.Group != "grp" || e.Source != "pm" {
		t.Errorf("status event = %+v", e)
	}
	if e := details["a-4|run-epic ended: failed (1 merged, 1 failed)"]; e.Severity != SeverityCrit {
		t.Errorf("failed run event = %+v", e)
	}
	for k := range details {
		if strings.HasPrefix(k, "a-3|") || strings.HasPrefix(k, "z-1|") || strings.Contains(k, "started") {
			t.Errorf("out-of-window or asleep event leaked: %s", k)
		}
	}
	found := false
	for _, e := range ch.Events {
		if strings.HasPrefix(e.Title, "focus plan for") {
			found = true
		}
	}
	if !found {
		t.Errorf("focus plan event missing: %v", keysOf(details))
	}
}

func keysOf(m map[string]Event) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// gitRepo builds a checkout with a task branch carrying one fresh and one
// old commit, so the --since window is exercised for real.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x")
		c.Env = append(c.Env, env...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(nil, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	run(nil, "add", ".")
	old := feedNow.AddDate(0, 0, -5).Format(time.RFC3339)
	run([]string{"GIT_AUTHOR_DATE=" + old, "GIT_COMMITTER_DATE=" + old}, "commit", "-q", "-m", "old base")
	run(nil, "checkout", "-q", "-b", "feat/task-branch")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	run(nil, "add", ".")
	fresh := feedNow.Add(-time.Hour).Format(time.RFC3339)
	run([]string{"GIT_AUTHOR_DATE=" + fresh, "GIT_COMMITTER_DATE=" + fresh}, "commit", "-q", "-m", "fresh work on the task")
	return dir
}

func TestGitSourceOnATempRepo(t *testing.T) {
	store := feedStore(t)
	repo := gitRepo(t)
	addRemote(t, repo, "https://github.com/x/y.git")
	if _, err := store.MutateProject("alpha", func(p *storage.Project) error { p.Path = repo; return nil }); err != nil {
		t.Fatal(err)
	}
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-1", Title: "the task", Status: storage.StatusDoing, Branch: "feat/task-branch", Created: "2026-09-01", Updated: at(time.Hour)})
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-2", Title: "no branch yet", Status: storage.StatusTodo, Branch: "feat/not-created", Created: "2026-09-01", Updated: at(time.Hour)})

	// gh is faked: not installed. Git itself is real.
	run := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "gh" {
			return "", fmt.Errorf("gh: %w", exec.ErrNotFound)
		}
		return DefaultRunner(ctx, dir, name, args...)
	}
	f := New(store.Root, []Source{&GitSource{Run: run}})
	res, err := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow)
	if err != nil {
		t.Fatal(err)
	}
	st := res.Sources[0]
	if !strings.Contains(st.Error, "alpha") || !strings.Contains(st.Error, "gh is not installed") {
		t.Fatalf("missing gh must be a per-project source error, got %q", st.Error)
	}
	if st.Events != 1 {
		t.Fatalf("events = %d (the fresh commit only)", st.Events)
	}
	ch, _ := f.Read(feedFrom)
	e := ch.Events[0]
	if e.TaskID != "a-1" || e.Title != "fresh work on the task" || !strings.Contains(e.Detail, "on feat/task-branch") || e.Source != "git" {
		t.Fatalf("commit event = %+v", e)
	}

	// A project without a checkout is silently skipped; a non-repo path is
	// a per-project error.
	if _, err := store.MutateProject("alpha", func(p *storage.Project) error { p.Path = t.TempDir(); return nil }); err != nil {
		t.Fatal(err)
	}
	res, _ = f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow)
	if !strings.Contains(res.Sources[0].Error, "not a git checkout") {
		t.Fatalf("non-repo path: %+v", res.Sources[0])
	}
}

func TestGitAndGitHubSourcesParseGH(t *testing.T) {
	store := feedStore(t)
	repo := gitRepo(t)
	addRemote(t, repo, "git@github.com-alias:x/y.git")
	if _, err := store.MutateProject("alpha", func(p *storage.Project) error { p.Path = repo; return nil }); err != nil {
		t.Fatal(err)
	}
	writeTask(t, store, "alpha", storage.TaskMeta{ID: "a-1", Title: "the task", Status: storage.StatusDoing, Branch: "feat/task-branch", Created: "2026-09-01", Updated: at(time.Hour)})
	prList := fmt.Sprintf(`[{"number":7,"title":"Add b","url":"https://gh/x/pull/7","updatedAt":%q,"createdAt":%q,"headRefName":"feat/task-branch","author":{"login":"me-login"},"isDraft":true,"reviewDecision":"CHANGES_REQUESTED"},
	{"number":3,"title":"Stale","url":"https://gh/x/pull/3","updatedAt":%q,"createdAt":%q,"headRefName":"other","author":{"login":"x"}},
	{"number":9,"title":"Merged one","url":"https://gh/x/pull/9","state":"MERGED","updatedAt":%[5]q,"createdAt":%[4]q,"mergedAt":%[5]q,"mergedBy":{"login":"lead"},"headRefName":"feat/task-branch","author":{"login":"me-login"}},
	{"number":10,"title":"Dropped","url":"https://gh/x/pull/10","state":"CLOSED","updatedAt":%[6]q,"createdAt":%[4]q,"closedAt":%[6]q,"headRefName":"feat/task-branch","author":{"login":"me-login"}}]`,
		at(30*time.Minute), at(30*time.Minute), at(72*time.Hour), at(96*time.Hour), at(10*time.Minute), at(5*time.Minute))
	prView9 := fmt.Sprintf(`{"number":9,"title":"Merged one","url":"https://gh/x/pull/9","comments":[
	{"id":"n1","body":"### PR preview published","createdAt":%[1]q,"author":{"login":"github-actions"}},
	{"id":"n2","body":"<!-- linear-linkback -->\nACME-1","createdAt":%[1]q,"author":{"login":"linear-code"}},
	{"id":"n3","body":"/preview","createdAt":%[1]q,"author":{"login":"me-login"}},
	{"id":"n4","body":"thanks, verified after the merge","createdAt":%[1]q,"author":{"login":"rev"}}],"reviews":[]}`, at(8*time.Minute))
	prView := fmt.Sprintf(`{"number":7,"title":"Add b","url":"https://gh/x/pull/7","reviewDecision":"CHANGES_REQUESTED",
	"comments":[{"id":"c1","body":"looks off\nsecond line","createdAt":%q,"url":"https://gh/x/pull/7#c1","author":{"login":"rev"}},{"id":"c0","body":"old","createdAt":%q,"author":{"login":"rev"}}],
	"reviews":[{"id":"r1","state":"CHANGES_REQUESTED","body":"","submittedAt":%q,"author":{"login":"rev"}}]}`, at(20*time.Minute), at(50*time.Hour), at(15*time.Minute))
	var calls []string
	run := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" {
			return DefaultRunner(ctx, dir, name, args...)
		}
		switch {
		case args[0] == "api":
			return "me-login\n", nil
		case args[1] == "list":
			return prList, nil
		case args[1] == "view" && args[2] == "7":
			return prView, nil
		case args[1] == "view" && args[2] == "9":
			return prView9, nil
		case args[1] == "view" && args[2] == "10":
			return `{"number":10,"title":"Dropped","comments":[],"reviews":[]}`, nil
		}
		return "", fmt.Errorf("unexpected gh call %v", args)
	}
	f := New(store.Root, []Source{&GitSource{Run: run}, &GitHubSource{Run: run}})
	res, err := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Sources {
		if s.Error != "" {
			t.Errorf("%s error = %q", s.Name, s.Error)
		}
	}
	ch, _ := f.Read(feedFrom)
	kinds := map[string]Event{}
	for _, e := range ch.Events {
		kinds[e.Source+":"+e.Detail] = e
	}
	if e, ok := kinds["git:PR #7 by me-login opened (draft)"]; !ok || e.TaskID != "a-1" || e.URL != "https://gh/x/pull/7" {
		t.Errorf("PR event missing/wrong: %v", keysOf(kinds))
	}
	if e, ok := kinds["github:PR #7 comment by rev: looks off"]; !ok || e.URL != "https://gh/x/pull/7#c1" {
		t.Errorf("comment event missing/wrong: %v", keysOf(kinds))
	}
	if e, ok := kinds["github:PR #7 review by rev: changes requested"]; !ok || e.Severity != SeverityWarn {
		t.Errorf("review event missing/wrong: %v", keysOf(kinds))
	}
	for k := range kinds {
		if strings.Contains(k, "#3") || strings.Contains(k, "old") {
			t.Errorf("out-of-window event leaked: %s", k)
		}
	}
	// The stale PR #3 was never opened with gh pr view.
	listedAll := false
	for _, c := range calls {
		if strings.Contains(c, "view 3") {
			t.Errorf("gh pr view on an untouched PR: %s", c)
		}
		if strings.Contains(c, "pr list --state all --search updated:>=") {
			listedAll = true
		}
	}
	if !listedAll {
		t.Errorf("PRs not listed in every state since the cutoff: %v", calls)
	}
	// A merge and a close in the window are events of their own (not
	// "updated"); talk under a merged PR is still read, machine chatter is not.
	if e, ok := kinds["git:PR #9 by me-login merged by lead"]; !ok || e.Severity != SeverityOK || e.TaskID != "a-1" {
		t.Errorf("merge event missing/wrong: %v", keysOf(kinds))
	}
	if _, ok := kinds["git:PR #10 by me-login closed without merging"]; !ok {
		t.Errorf("close event missing: %v", keysOf(kinds))
	}
	if _, ok := kinds["github:PR #9 comment by rev: thanks, verified after the merge"]; !ok {
		t.Errorf("comment under a merged PR missing: %v", keysOf(kinds))
	}
	for k := range kinds {
		if strings.Contains(k, "#9 by me-login updated") || strings.Contains(k, "github-actions") || strings.Contains(k, "linear-code") || strings.Contains(k, "/preview") {
			t.Errorf("noise or duplicate event: %s", k)
		}
	}
	// The snapshot says where each PR stands now.
	states := map[int]PRState{}
	for _, s := range f.PRStates(feedFrom) {
		states[s.Number] = s
	}
	if states[9].State != "merged" || states[9].TaskID != "a-1" || states[7].State != "open" || !states[7].Draft || states[10].State != "closed" {
		t.Errorf("PR states = %+v", states)
	}
	// A later refresh that lists nothing keeps the known states.
	stateAt := time.Now()
	if err := f.mergePRStates(nil, stateAt); err != nil {
		t.Fatal(err)
	}
	if len(f.PRStates(feedFrom)) < 3 {
		t.Errorf("snapshot lost rows: %+v", f.PRStates(feedFrom))
	}
}

func TestNoiseComment(t *testing.T) {
	for _, c := range []struct {
		login, body string
		want        bool
	}{
		{"github-actions", "# Performance Comparison Report", true},
		{"linear-code", "<!-- linear-linkback -->\nACME-1", true},
		{"someone", "<!-- linear-linkback --> moved", true},
		{"me-login", "/preview", true},
		{"me-login", "  /deploy staging  ", true},
		{"claude", "Found 2 issues", false},
		{"claude[bot]", "### Code review", false},
		{"reviewer-two", "@me-login I'm testing the PR preview", false},
		{"rev", "/src/app.ts is wrong because the zone is dropped here", false},
		{"rev", "/preview\nand also this looks off", false},
	} {
		if got := noiseComment(c.login, c.body); got != c.want {
			t.Errorf("noiseComment(%q, %q) = %v", c.login, c.body, got)
		}
	}
}

func addRemote(t *testing.T, repo, url string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", url).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v %s", err, out)
	}
}

// TestGitHubContext: a checkout with no GitHub remote is skipped without an
// error (GitLab, no remote at all), and gh_account puts that account's token
// on the context every later gh call runs with.
func TestGitHubContext(t *testing.T) {
	var ghCalls []string
	var listEnv []string
	tokenErr := false
	run := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "git" {
			return DefaultRunner(ctx, dir, name, args...)
		}
		ghCalls = append(ghCalls, strings.Join(args, " "))
		switch {
		case args[0] == "auth" && tokenErr:
			return "", fmt.Errorf("gh: exit status 1: no account")
		case args[0] == "auth":
			return "tok-123\n", nil
		case args[1] == "list":
			listEnv = EnvFrom(ctx)
			return "[]", nil
		}
		return "", fmt.Errorf("unexpected gh call %v", args)
	}

	none := gitRepo(t)
	gitlab := gitRepo(t)
	addRemote(t, gitlab, "git@gitlab.com:group/repo.git")
	for name, path := range map[string]string{"no remote": none, "gitlab": gitlab} {
		for _, src := range []Source{&GitSource{Run: run}, &GitHubSource{Run: run}} {
			_, err := src.Fetch(context.Background(), feedFrom, feedNow, []Project{{Slug: "p", Path: path}})
			if err != nil {
				t.Errorf("%s/%s: want no error, got %v", name, src.Name(), err)
			}
		}
	}
	if len(ghCalls) != 0 {
		t.Fatalf("gh called for a repo without a GitHub remote: %v", ghCalls)
	}

	hub := gitRepo(t)
	addRemote(t, hub, "https://github.com/x/y.git")
	p := Project{Slug: "p", Path: hub, GHAccount: "work"}
	if _, err := (&GitHubSource{Run: run}).Fetch(context.Background(), feedFrom, feedNow, []Project{p}); err != nil {
		t.Fatal(err)
	}
	if len(ghCalls) < 1 || ghCalls[0] != "auth token --user work" {
		t.Fatalf("gh calls = %v", ghCalls)
	}
	if strings.Join(listEnv, " ") != "GH_TOKEN=tok-123" {
		t.Fatalf("gh pr list env = %v", listEnv)
	}

	tokenErr = true
	_, err := (&GitSource{Run: run}).Fetch(context.Background(), feedFrom, feedNow, []Project{p})
	if err == nil || !strings.Contains(err.Error(), "gh_account work") {
		t.Fatalf("a failing token lookup must name the account, got %v", err)
	}
}

// TestOpenPRsOnlyTheUsers: both PR sources drop a PR by someone else with no
// review request to the user and a non-task head (never `gh pr view` it),
// and keep the user's own PR, a PR with a review requested from the user and
// a stranger's PR on a task branch. gh_account is the login as is; without
// it `gh api user` is asked once per project.
func TestOpenPRsOnlyTheUsers(t *testing.T) {
	listFor := func(login string) string {
		return fmt.Sprintf(`[
		{"number":1,"title":"Theirs","updatedAt":%[1]q,"createdAt":%[2]q,"headRefName":"feat/random","author":{"login":"casey"},"reviewRequests":[{"login":"somebody"},{"name":"team"}]},
		{"number":2,"title":"Mine","updatedAt":%[1]q,"createdAt":%[2]q,"headRefName":"feat/mine","author":{"login":%[3]q}},
		{"number":3,"title":"Review me","updatedAt":%[1]q,"createdAt":%[2]q,"headRefName":"feat/review","author":{"login":"vernon"},"reviewRequests":[{"login":%[4]q}]},
		{"number":4,"title":"On my task","updatedAt":%[1]q,"createdAt":%[2]q,"headRefName":"feat/task-branch","author":{"login":"vernon"}}]`,
			at(30*time.Minute), at(40*time.Minute), login, strings.ToUpper(login))
	}
	var calls []string
	run := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "git" {
			return DefaultRunner(ctx, dir, name, args...)
		}
		calls = append(calls, dir+": "+strings.Join(args, " "))
		switch {
		case args[0] == "auth":
			return "tok\n", nil
		case args[0] == "api":
			return "me-login\n", nil
		case args[1] == "list":
			if len(EnvFrom(ctx)) > 0 {
				return listFor("Work-Account"), nil // the gh_account project
			}
			return listFor("me-login"), nil
		case args[1] == "view":
			return fmt.Sprintf(`{"number":%s,"title":"t","comments":[{"id":"c","body":"hi","createdAt":%q,"author":{"login":"rev"}}]}`, args[2], at(10*time.Minute)), nil
		}
		return "", fmt.Errorf("unexpected gh call %v", args)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "a-1", Branch: "feat/task-branch"}}
	active, account := gitRepo(t), gitRepo(t)
	addRemote(t, active, "https://github.com/x/active.git")
	addRemote(t, account, "https://github.com/x/account.git")
	projects := []Project{
		{Slug: "active", Path: active, Tasks: []*storage.Task{task}},
		{Slug: "account", Path: account, Tasks: []*storage.Task{task}, GHAccount: "work-account"},
	}

	for _, src := range []Source{&GitSource{Run: run}, &GitHubSource{Run: run}} {
		calls = nil
		events, err := src.Fetch(context.Background(), feedFrom, feedNow, projects)
		if err != nil {
			t.Fatalf("%s: %v", src.Name(), err)
		}
		got := map[string]map[string]bool{}
		for _, e := range events {
			if got[e.Project] == nil {
				got[e.Project] = map[string]bool{}
			}
			for _, n := range []string{"1", "2", "3", "4"} {
				if strings.HasPrefix(e.Detail, "PR #"+n+" ") {
					got[e.Project][n] = true
				}
			}
		}
		for _, slug := range []string{"active", "account"} {
			for _, n := range []string{"2", "3", "4"} {
				if !got[slug][n] {
					t.Errorf("%s/%s: PR #%s dropped, want kept (events %+v)", src.Name(), slug, n, events)
				}
			}
			if got[slug]["1"] {
				t.Errorf("%s/%s: someone else's PR #1 kept", src.Name(), slug)
			}
		}
		apiCalls := map[string]int{}
		for _, c := range calls {
			if strings.HasSuffix(c, "view 1 --json "+prViewFields) {
				t.Errorf("%s: gh pr view on a dropped PR: %s", src.Name(), c)
			}
			if strings.Contains(c, ": api user") {
				apiCalls[strings.SplitN(c, ":", 2)[0]]++
			}
		}
		if apiCalls[active] != 1 || apiCalls[account] != 0 {
			t.Errorf("%s: gh api user calls = %v, want once for the project without gh_account only", src.Name(), apiCalls)
		}
	}

	// A failing login lookup is that project's error; the other still runs.
	failing := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "gh" && args[0] == "api" {
			return "", fmt.Errorf("gh: exit status 1: not logged in")
		}
		return run(ctx, dir, name, args...)
	}
	events, err := (&GitSource{Run: failing}).Fetch(context.Background(), feedFrom, feedNow, projects)
	var pe *ProjectError
	if !errors.As(err, &pe) || pe.Project != "active" || !strings.Contains(err.Error(), "gh api user") {
		t.Fatalf("login failure = %v", err)
	}
	accountPRs := 0
	for _, e := range events {
		if !strings.HasPrefix(e.Detail, "PR #") {
			continue // commits on the fixture's task branch are not PRs
		}
		if e.Project == "active" {
			t.Errorf("PR of a project whose login failed leaked: %+v", e)
		} else {
			accountPRs++
		}
	}
	if accountPRs != 3 {
		t.Errorf("the gh_account project's PRs = %d events, want 3", accountPRs)
	}
}

// TestReadDoesNotWaitForRefresh: the cache lock is not held across the
// sources, so the Changes screen (Read) and "mark seen" (MarkSeen) answer
// while a slow source runs - and a MarkSeen landing mid-refresh is still in
// the state the refresh writes at its end.
func TestReadDoesNotWaitForRefresh(t *testing.T) {
	store := feedStore(t)
	slow := &fakeSource{name: "pm", slow: 600 * time.Millisecond, events: []Event{{ID: "a", TS: at(time.Hour), Title: "a"}}}
	f := New(store.Root, []Source{slow})
	done := make(chan *Result, 1)
	go func() {
		res, _ := f.Refresh(context.Background(), store, cfgWith(nil), feedFrom, feedNow)
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	if _, err := f.Read(feedFrom); err != nil {
		t.Fatal(err)
	}
	seenAt := feedNow.Add(-30 * time.Minute)
	if err := f.MarkSeen(seenAt); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited > 300*time.Millisecond {
		t.Fatalf("Read+MarkSeen waited %v for the refresh", waited)
	}
	select {
	case res := <-done:
		if res == nil || res.Added != 1 {
			t.Fatalf("refresh = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
	ch, err := f.Read(feedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Seen != seenAt.Format(time.RFC3339) {
		t.Fatalf("the seen mark set during the refresh was lost: %q", ch.Seen)
	}
	if len(ch.Sources) != 1 || ch.Sources[0].Events != 1 {
		t.Fatalf("sources = %+v", ch.Sources)
	}
}
