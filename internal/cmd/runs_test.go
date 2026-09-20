package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// fakeSSH puts a stub `ssh` at the front of PATH - the same seam fakeClaude
// uses, and the reason fetchRemoteRuns resolves ssh from PATH rather than from
// an absolute path. The stub also records that it RAN, so a test can assert
// that --local reached no machine at all.
func fakeSSH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	body := "#!/bin/sh\necho \"$@\" >> " + marker + "\n" + script
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func sshRan(t *testing.T, marker string) string {
	t.Helper()
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read ssh marker: %v", err)
	}
	return string(data)
}

// runsFixture: a store with one project, one tracker (a parent + one child) and
// a done run-state for it, so every runs test has at least one local row to
// prove was not lost.
func runsFixture(t *testing.T) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("app", &storage.Project{Name: "App", Prefix: "app", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []storage.TaskMeta{
		{ID: "app-1", Title: "Local batch", Status: storage.StatusDoing, Created: "2026-08-01", Updated: "2026-08-01"},
		{ID: "app-1-1", Title: "sub", Status: storage.StatusTodo, Created: "2026-08-01", Updated: "2026-08-01", Parent: "app-1"},
	} {
		if err := store.AddTask("app", &storage.Task{Meta: meta}); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.WriteRunState(store.ProjectDir("app"), &storage.RunState{
		TaskID: "app-1", Project: "app", Kind: storage.RunKindEpic, Status: storage.RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-01T10:00:00Z",
		Subs: []storage.SubRun{{ID: "app-1-1", Status: "merged"}},
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

// ageRunState back-dates a run-state's `updated` stamp. WriteRunState stamps it
// with the current time by design, so a test about ORDER has to write the file
// itself - there is no other way to have a local row that is older than a
// remote one.
func ageRunState(t *testing.T, store *storage.Store, slug, taskID, stamp string) {
	t.Helper()
	path := storage.ExecutorRunPath(store.ProjectDir(slug), taskID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run state: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse run state: %v", err)
	}
	raw["updated"] = stamp
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("write run state: %v", err)
	}
}

func runRunsCmd(t *testing.T, store storage.TaskStore, args ...string) (string, error) {
	t.Helper()
	cmd := newRunsCmd(store)
	var out strings.Builder
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

func parseRunsJSON(t *testing.T, out string) runsPayload {
	t.Helper()
	var payload runsPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out)
	}
	return payload
}

func TestRunsTableLocal(t *testing.T) {
	store := runsFixture(t)
	out, err := runRunsCmd(t, store, "--local")
	if err != nil {
		t.Fatalf("pm runs --local: %v\n%s", err, out)
	}
	for _, want := range []string{"PROJECT", "TRACKER", "ACCEPTANCE", "app-1", "Local batch", "done 1/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// --local is the flag the REMOTE side is invoked with, so it must be provably
// incapable of reaching another machine: without it, two machines listing each
// other would recurse.
func TestRunsLocalContactsNoRemote(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	marker := fakeSSH(t, "echo '{\"rows\":[]}'\n")

	out, err := runRunsCmd(t, store, "--local")
	if err != nil {
		t.Fatalf("pm runs --local: %v\n%s", err, out)
	}
	if ran := sshRan(t, marker); ran != "" {
		t.Errorf("--local shelled out to ssh: %q", ran)
	}
	if !strings.Contains(out, "app-1") {
		t.Errorf("local rows missing:\n%s", out)
	}
}

func TestRunsLocalAndRemoteFlagsConflict(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	if _, err := runRunsCmd(t, store, "--local", "--remote", "nimbus"); err == nil {
		t.Error("--local with --remote must be refused, not silently resolved one way")
	}
}

// A sleeping VPS costs ONE row with a note. Not an error, not the local rows.
func TestRunsUnreachableRemoteIsANote(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	fakeSSH(t, "echo 'ssh: connect to host nimbus port 22: Operation timed out' >&2\nexit 255\n")

	out, err := runRunsCmd(t, store, "--json")
	if err != nil {
		t.Fatalf("an unreachable remote must not fail the command: %v\n%s", err, out)
	}
	payload := parseRunsJSON(t, out)

	var note, local *storage.RunRow
	for i := range payload.Rows {
		if payload.Rows[i].Note != "" {
			note = &payload.Rows[i]
		}
		if payload.Rows[i].Tracker == "app-1" {
			local = &payload.Rows[i]
		}
	}
	if local == nil {
		t.Errorf("the local rows were lost with the remote:\n%s", out)
	}
	if note == nil {
		t.Fatalf("no note row for the unreachable remote:\n%s", out)
	}
	if note.Remote != "nimbus" {
		t.Errorf("note row remote = %q, want nimbus", note.Remote)
	}
	// A placeholder row must not invent a project slug - a consumer filtering
	// rows by project would match the note against a real one.
	if note.Project != "" {
		t.Errorf("note row project = %q, want empty", note.Project)
	}
	if !strings.Contains(note.Note, "unreachable") {
		t.Errorf("note %q does not say the machine could not be reached", note.Note)
	}
	// The note must carry the reason, not just the fact.
	if !strings.Contains(note.Note, "connect to host") {
		t.Errorf("note %q drops ssh's own message", note.Note)
	}
}

// An older pm on the far side has no `runs` command: cobra prints "unknown
// command" to STDERR and the process exits non-zero, which ssh passes through
// (255 is reserved for ssh's own failures). That is a note - and it must not
// call the machine unreachable, because it answered.
func TestRunsRemotePmTooOldIsANote(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	fakeSSH(t, "echo 'Error: unknown command \"runs\" for \"pm\"' >&2\nexit 1\n")

	out, err := runRunsCmd(t, store)
	if err != nil {
		t.Fatalf("an old remote pm must not fail the command: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote pm failed") || !strings.Contains(out, "too old") {
		t.Errorf("table does not report the version skew:\n%s", out)
	}
	if strings.Contains(out, "unreachable") {
		t.Errorf("a machine that answered must not be called unreachable:\n%s", out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("the note drops what the remote actually said:\n%s", out)
	}
	if !strings.Contains(out, "app-1") {
		t.Errorf("local rows lost:\n%s", out)
	}
}

// A remote that exits 0 and still does not produce JSON (a login shell or a
// wrapper printing over pm's stdout): a note too, never a panic.
func TestRunsRemoteGarbageIsANote(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	fakeSSH(t, "echo 'Welcome to nimbus!'\nexit 0\n")

	out, err := runRunsCmd(t, store)
	if err != nil {
		t.Fatalf("garbage from a remote must not fail the command: %v\n%s", err, out)
	}
	if !strings.Contains(out, "unparsable") {
		t.Errorf("table does not report the unparsable answer:\n%s", out)
	}
	if !strings.Contains(out, "Welcome to nimbus!") {
		t.Errorf("the note drops what the remote actually said:\n%s", out)
	}
	if !strings.Contains(out, "app-1") {
		t.Errorf("local rows lost:\n%s", out)
	}
}

// The remote's own rows arrive over --json and are stamped with the remote's
// name, because the same tracker id can exist on both machines.
func TestRunsRemoteRowsAreQualified(t *testing.T) {
	store := runsFixture(t)
	ageRunState(t, store, "app", "app-1", "2026-08-01T10:00:00Z")
	writeStoreConfig(t, store, configWithRemote)
	remote := `{"rows":[{"project":"orbit","tracker":"app-1","title":"VPS batch",` +
		`"updated":"2026-08-07T10:00:00Z","run":{"state":"running","done":3,"total":8},` +
		`"acceptance":{"state":"done","visual_claims_open":2}}]}`
	fakeSSH(t, "echo '"+remote+"'\n")

	out, err := runRunsCmd(t, store)
	if err != nil {
		t.Fatalf("pm runs: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nimbus/orbit") {
		t.Errorf("remote row is not qualified with the remote name:\n%s", out)
	}
	if !strings.Contains(out, "running 3/8") {
		t.Errorf("remote RUN cell missing:\n%s", out)
	}
	if !strings.Contains(out, "2 visual claim(s) open") {
		t.Errorf("remote open visual claims missing:\n%s", out)
	}
	// Newest activity first: the remote row's 2026-08-07 beats the local
	// 2026-08-01, so the local tracker id must appear BELOW it even though both
	// rows carry the id app-1.
	if strings.Index(out, "nimbus/orbit") > strings.Index(out, "\napp") {
		t.Errorf("rows are not ordered newest first:\n%s", out)
	}
}

// The far side is always invoked with --json --local, and with a connect
// timeout: the recursion guard and the sleeping-VPS guard are both in the argv.
func TestRunsRemoteInvocation(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	marker := fakeSSH(t, "echo '{\"rows\":[]}'\n")

	if out, err := runRunsCmd(t, store); err != nil {
		t.Fatalf("pm runs: %v\n%s", err, out)
	}
	ran := sshRan(t, marker)
	for _, want := range []string{"ConnectTimeout=5", "BatchMode=yes", "nimbus", "/home/runner/go/bin/pm", "runs", "--json", "--local"} {
		if !strings.Contains(ran, want) {
			t.Errorf("ssh argv %q missing %q", ran, want)
		}
	}
}

func TestRunsUnknownRemoteErrors(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	fakeSSH(t, "echo '{\"rows\":[]}'\n")

	out, err := runRunsCmd(t, store, "--remote", "nope")
	if err == nil {
		t.Fatalf("want an error for an unknown remote, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nimbus") {
		t.Errorf("error %q does not name the remotes that ARE declared", err)
	}
}

// A broken global config fails the command: reading "you have no remotes" off a
// YAML typo is the silent degradation storage.LoadConfig refuses.
func TestRunsBrokenConfigErrors(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, "remotes:\n  - name: runner\n   ssh: broken\n")
	if _, err := runRunsCmd(t, store); err == nil {
		t.Error("a broken config must fail the command")
	}
	// ...but --local never reads it: the local view must survive a broken
	// registry it does not need.
	if out, err := runRunsCmd(t, store, "--local"); err != nil {
		t.Errorf("--local must not care about the remote registry: %v\n%s", err, out)
	}
}

// The --json shape is what the board stands on, so it is pinned: an object with
// a `rows` array, `rows: []` (never null) when there is nothing, and the
// per-row keys the board reads.
func TestRunsJSONShape(t *testing.T) {
	store := runsFixture(t)
	out, err := runRunsCmd(t, store, "--local", "--json")
	if err != nil {
		t.Fatalf("pm runs --json: %v\n%s", err, out)
	}
	payload := parseRunsJSON(t, out)
	if len(payload.Rows) != 1 {
		t.Fatalf("want 1 row, got %d:\n%s", len(payload.Rows), out)
	}
	row := payload.Rows[0]
	if row.Project != "app" || row.Tracker != "app-1" || row.Title != "Local batch" {
		t.Errorf("row identity wrong: %+v", row)
	}
	if row.Run.State != storage.RunCellDone || row.Run.Done != 1 || row.Run.Total != 1 {
		t.Errorf("run cell wrong: %+v", row.Run)
	}
	// The keys, not just the parsed struct - a rename would break the board.
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	rows, ok := raw["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("top level is not {rows: [...]}: %s", out)
	}
	first, _ := rows[0].(map[string]any)
	for _, key := range []string{"project", "tracker", "title", "run", "acceptance"} {
		if _, ok := first[key]; !ok {
			t.Errorf("row is missing key %q: %s", key, out)
		}
	}
	run, _ := first["run"].(map[string]any)
	for _, key := range []string{"state", "done", "total"} {
		if _, ok := run[key]; !ok {
			t.Errorf("run cell is missing key %q: %s", key, out)
		}
	}
}

// Nothing to show is an empty array, not null: a script or the board iterating
// `rows` must not have to special-case it.
func TestRunsJSONEmptyIsArray(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	out, err := runRunsCmd(t, store, "--local", "--json")
	if err != nil {
		t.Fatalf("pm runs --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"rows": []`) {
		t.Errorf("empty listing must encode as rows: []:\n%s", out)
	}
}

func TestRunsProjectFilter(t *testing.T) {
	store := runsFixture(t)
	if err := store.CreateProject("other", &storage.Project{Name: "Other", Prefix: "oth", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []storage.TaskMeta{
		{ID: "oth-1", Title: "Other batch", Status: storage.StatusDoing, Created: "2026-08-02", Updated: "2026-08-02"},
		{ID: "oth-1-1", Title: "sub", Status: storage.StatusTodo, Created: "2026-08-02", Updated: "2026-08-02", Parent: "oth-1"},
	} {
		if err := store.AddTask("other", &storage.Task{Meta: meta}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runRunsCmd(t, store, "--local", "--project", "other")
	if err != nil {
		t.Fatalf("pm runs --project: %v\n%s", err, out)
	}
	if !strings.Contains(out, "oth-1") {
		t.Errorf("the requested project is missing:\n%s", out)
	}
	if strings.Contains(out, "app-1") {
		t.Errorf("--project did not narrow the listing:\n%s", out)
	}
}

// --project also narrows the REMOTE rows, and it does so on the slug the remote
// reported rather than by asking the remote to resolve a name against its own
// project list.
func TestRunsProjectFilterAppliesToRemoteRows(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	remote := `{"rows":[` +
		`{"project":"app","tracker":"app-7","title":"VPS app batch","updated":"2026-08-07T10:00:00Z","run":{"state":"done","done":1,"total":1}},` +
		`{"project":"orbit","tracker":"orbit-9","title":"VPS other","updated":"2026-08-07T11:00:00Z","run":{"state":"done","done":1,"total":1}}]}`
	fakeSSH(t, "echo '"+remote+"'\n")

	// An ABBREVIATION, not the exact slug: the filter runs on the resolved slug,
	// so `-p ap` must keep the remote rows for `app` rather than silently
	// dropping every one of them.
	out, err := runRunsCmd(t, store, "--project", "ap")
	if err != nil {
		t.Fatalf("pm runs --project: %v\n%s", err, out)
	}
	if !strings.Contains(out, "app-7") {
		t.Errorf("the remote row for the requested project is missing:\n%s", out)
	}
	if strings.Contains(out, "orbit-9") {
		t.Errorf("--project did not narrow the remote rows:\n%s", out)
	}
}

// A remote whose answer is complete but which leaves a descendant holding the
// output pipe: Wait unblocks on WaitDelay with exec.ErrWaitDelay, and that is
// success-with-leftovers, not an unreachable machine. Throwing the answer away
// there would drop rows that had already arrived.
func TestRunsRemoteLeftoverDescendantStillCounts(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	remote := `{"rows":[{"project":"orbit","tracker":"orbit-9","title":"VPS batch",` +
		`"updated":"2026-08-07T10:00:00Z","run":{"state":"done","done":1,"total":1}}]}`
	// The answer is printed and ssh exits 0, but a background child keeps the
	// inherited stdout open past the exit.
	fakeSSH(t, "echo '"+remote+"'\nsleep 20 &\nexit 0\n")
	oldWait, oldTimeout := procWaitDelay, remoteRunsTimeout
	procWaitDelay, remoteRunsTimeout = 300*time.Millisecond, 10*time.Second
	defer func() { procWaitDelay, remoteRunsTimeout = oldWait, oldTimeout }()

	out, err := runRunsCmd(t, store)
	if err != nil {
		t.Fatalf("pm runs: %v\n%s", err, out)
	}
	if !strings.Contains(out, "orbit-9") {
		t.Errorf("a complete answer was discarded over a leftover descendant:\n%s", out)
	}
	if strings.Contains(out, "unreachable") {
		t.Errorf("the machine answered in full and must not be called unreachable:\n%s", out)
	}
}

// A remote that accepts the connection and then says nothing must not hang the
// listing: ConnectTimeout does not cover it, remoteRunsTimeout does.
func TestRunsRemoteHangIsANote(t *testing.T) {
	store := runsFixture(t)
	writeStoreConfig(t, store, configWithRemote)
	fakeSSH(t, "sleep 30\n")
	old := remoteRunsTimeout
	remoteRunsTimeout = 300 * time.Millisecond
	defer func() { remoteRunsTimeout = old }()

	start := time.Now()
	out, err := runRunsCmd(t, store)
	if err != nil {
		t.Fatalf("a silent remote must not fail the command: %v\n%s", err, out)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the listing waited %s on a silent remote", elapsed)
	}
	if !strings.Contains(out, "no answer within") {
		t.Errorf("table does not report the timeout:\n%s", out)
	}
	if !strings.Contains(out, "app-1") {
		t.Errorf("local rows lost:\n%s", out)
	}
}
