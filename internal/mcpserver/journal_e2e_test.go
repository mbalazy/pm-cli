package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// declareJournals rewrites the test project with a `journals:` list - the only
// thing that makes a journal exist.
func declareJournals(t *testing.T, store *storage.Store, subjects ...storage.JournalSubject) {
	t.Helper()
	path := filepath.Join(store.Root, "test", "project.yaml")
	proj, err := storage.ReadProject(path)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	proj.Journals = subjects
	if err := storage.WriteProject(path, proj); err != nil {
		t.Fatalf("write project: %v", err)
	}
}

type journalAddResult struct {
	Project string           `json:"project"`
	Journal string           `json:"journal"`
	Entry   storage.Incident `json:"entry"`
	Total   int              `json:"total"`
	Open    int              `json:"open"`
	Note    string           `json:"note"`
}

func TestE2EJournalAddAndList(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig", Subject: "iOS simulator rig"})
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_journal_add", map[string]any{
		"project":          "test",
		"name":             "sim-rig",
		"symptom":          "rig-check printed RIG DEAD on a live rig",
		"false_conclusion": "the simulator was not running my code",
		"cause":            "it took the first booted sim, which served the other worktree",
		"cost_min":         20,
		"tags":             []string{"wrong-sim"},
	})
	if isErr {
		t.Fatalf("journal_add error: %s", text)
	}
	var added journalAddResult
	mustUnmarshal(t, text, &added)
	if added.Total != 1 || added.Open != 1 {
		t.Fatalf("counts after first entry: %+v", added)
	}
	if added.Entry.TS == "" {
		t.Fatal("TS was not stamped")
	}
	// The write must report back that this one is open - that is what makes
	// the fix get recorded later instead of the entry quietly rotting.
	if !strings.Contains(added.Note, "OPEN") {
		t.Fatalf("note does not flag the entry as open: %q", added.Note)
	}

	// A second entry sharing the tag: the note must say the cluster grew,
	// because a cluster is the argument for fixing the tool.
	text, isErr = call(t, sess, "pm_journal_add", map[string]any{
		"project": "test", "name": "sim-rig",
		"symptom": "drove the wrong sim again",
		"tags":    []string{"wrong-sim"},
		"date":    "2026-07-15",
	})
	if isErr {
		t.Fatalf("journal_add error: %s", text)
	}
	mustUnmarshal(t, text, &added)
	if !strings.Contains(added.Note, `2 entries now share tag "wrong-sim"`) {
		t.Fatalf("recurrence not reported: %q", added.Note)
	}
	if !strings.HasPrefix(added.Entry.TS, "2026-07-15") {
		t.Fatalf("--date not honoured: %q", added.Entry.TS)
	}

	// Read back.
	text, isErr = call(t, sess, "pm_journal_list", map[string]any{"project": "test", "name": "sim-rig"})
	if isErr {
		t.Fatalf("journal_list error: %s", text)
	}
	var listed journalEntriesOutput
	mustUnmarshal(t, text, &listed)
	if listed.Total != 2 || listed.Open != 2 || listed.Shown != 2 {
		t.Fatalf("list rollup: %+v", listed)
	}
	if listed.Subject != "iOS simulator rig" {
		t.Fatalf("subject not carried: %q", listed.Subject)
	}
	// Newest first: the back-dated entry is second even though it was written last.
	if !strings.Contains(listed.Entries[0].Symptom, "RIG DEAD") {
		t.Fatalf("not newest first: %+v", listed.Entries)
	}
	if len(listed.Tags) != 1 || listed.Tags[0].Count != 2 {
		t.Fatalf("tag rollup missing from the read: %+v", listed.Tags)
	}
	if listed.CostMin != 20 {
		t.Fatalf("CostMin = %d, want 20", listed.CostMin)
	}
}

func TestE2EJournalListWithoutNameListsDeclared(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store,
		storage.JournalSubject{Name: "sim-rig", Subject: "iOS simulator rig"},
		storage.JournalSubject{Name: "flaky-e2e", Subject: "the e2e suite"},
	)
	sess := startMCP(t, store)

	if _, isErr := call(t, sess, "pm_journal_add", map[string]any{
		"project": "test", "name": "sim-rig", "symptom": "x", "fix": "done",
	}); isErr {
		t.Fatal("seed failed")
	}

	text, isErr := call(t, sess, "pm_journal_list", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("journal_list error: %s", text)
	}
	var out journalEntriesOutput
	mustUnmarshal(t, text, &out)
	if len(out.Declared) != 2 {
		t.Fatalf("declared: %+v", out.Declared)
	}
	if out.Declared[0].Name != "sim-rig" || out.Declared[0].Total != 1 || out.Declared[0].Open != 0 {
		t.Fatalf("first journal: %+v", out.Declared[0])
	}
	// A declared-but-never-written journal is REPORTED, not omitted - it is
	// the one someone still has to start using.
	if out.Declared[1].Name != "flaky-e2e" || out.Declared[1].Total != 0 {
		t.Fatalf("empty journal not reported: %+v", out.Declared[1])
	}
}

// An undeclared name must be a tool error, never an implicit create: an agent
// inventing a plausible name would start a second history nobody reads.
func TestE2EJournalAddRejectsUndeclaredName(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig"})
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_journal_add", map[string]any{
		"project": "test", "name": "sim-rgi", "symptom": "typo'd journal",
	})
	if !isErr {
		t.Fatalf("expected a tool error, got: %s", text)
	}
	if !strings.Contains(text, "sim-rig") {
		t.Fatalf("error must list the declared journals: %s", text)
	}
	// And nothing was written under the wrong name.
	if got, _ := storage.ReadIncidents(store.ProjectDir("test"), "sim-rgi"); len(got) != 0 {
		t.Fatalf("a rejected name still produced entries: %+v", got)
	}
}

// symptom is required in the generated schema, so an entry without one is
// refused by the SDK before the handler ever runs - a stronger guarantee than
// a handler check, and the reason this test bypasses the `call` helper (which
// treats a protocol-level rejection as fatal). The storage layer keeps its own
// check for the callers that do not come through MCP.
func TestE2EJournalAddRequiresSymptom(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig"})
	sess := startMCP(t, store)

	_, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "pm_journal_add",
		Arguments: map[string]any{"project": "test", "name": "sim-rig", "cause": "known, but never described"},
	})
	if err == nil {
		t.Fatal("expected the schema to reject an entry with no symptom")
	}
	if !strings.Contains(err.Error(), "symptom") {
		t.Fatalf("error should name the missing field: %v", err)
	}
	if got, _ := storage.ReadIncidents(store.ProjectDir("test"), "sim-rig"); len(got) != 0 {
		t.Fatalf("entry written despite a rejected call: %+v", got)
	}
}

func TestE2EJournalAddRejectsBadDate(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig"})
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_journal_add", map[string]any{
		"project": "test", "name": "sim-rig", "symptom": "x", "date": "03-08-2026",
	})
	if !isErr {
		t.Fatalf("expected a tool error, got: %s", text)
	}
	if !strings.Contains(text, "YYYY-MM-DD") {
		t.Fatalf("error should state the expected shape: %s", text)
	}
	if got, _ := storage.ReadIncidents(store.ProjectDir("test"), "sim-rig"); len(got) != 0 {
		t.Fatalf("entry written despite a rejected date: %+v", got)
	}
}

func TestE2EJournalListOpenFilterAndLimit(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig"})
	sess := startMCP(t, store)

	for _, e := range []struct {
		symptom, fix, date string
	}{
		{"fixed one", "rig-check resolves via Metro", "2026-07-01"},
		{"open one", "", "2026-07-02"},
		{"another open one", "", "2026-07-03"},
	} {
		args := map[string]any{"project": "test", "name": "sim-rig", "symptom": e.symptom, "date": e.date}
		if e.fix != "" {
			args["fix"] = e.fix
		}
		if _, isErr := call(t, sess, "pm_journal_add", args); isErr {
			t.Fatalf("seed %q failed", e.symptom)
		}
	}

	text, _ := call(t, sess, "pm_journal_list", map[string]any{"project": "test", "name": "sim-rig", "open": true})
	var out journalEntriesOutput
	mustUnmarshal(t, text, &out)
	if out.Shown != 2 {
		t.Fatalf("open filter returned %d entries: %+v", out.Shown, out.Entries)
	}
	// Total stays the WHOLE journal even when the list is filtered - the
	// filter narrows what you read, not what happened.
	if out.Total != 3 {
		t.Fatalf("Total = %d, want 3 (the filter must not shrink history)", out.Total)
	}

	text, _ = call(t, sess, "pm_journal_list", map[string]any{"project": "test", "name": "sim-rig", "limit": 1})
	mustUnmarshal(t, text, &out)
	if out.Shown != 1 {
		t.Fatalf("limit ignored: %+v", out)
	}
	// Silent truncation would read as "that is the whole history" - the exact
	// wrong conclusion for a record whose purpose is showing recurrence.
	if !strings.Contains(out.Note, "1 of 3") {
		t.Fatalf("truncation not disclosed: %q", out.Note)
	}
}

// pm_context runs at every session start, so it must carry the journal COUNTS
// and nothing else - a journal only grows, and its content belongs to the
// sessions that pull it.
func TestE2EContextCarriesJournalPointerNotContent(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	declareJournals(t, store, storage.JournalSubject{Name: "sim-rig", Subject: "iOS simulator rig"})
	sess := startMCP(t, store)

	if _, isErr := call(t, sess, "pm_journal_add", map[string]any{
		"project": "test", "name": "sim-rig",
		"symptom": "a very distinctive symptom string",
		"cause":   "an equally distinctive cause string",
	}); isErr {
		t.Fatal("seed failed")
	}

	text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}
	var ctx struct {
		Journals []storage.JournalCount `json:"journals"`
		Note     string                 `json:"journals_note"`
	}
	mustUnmarshal(t, text, &ctx)
	if len(ctx.Journals) != 1 || ctx.Journals[0].Total != 1 || ctx.Journals[0].Open != 1 {
		t.Fatalf("journal counts missing from context: %+v", ctx.Journals)
	}
	if ctx.Note == "" {
		t.Fatal("journals_note missing - the counts alone do not say what to do with them")
	}
	if strings.Contains(text, "a very distinctive symptom string") || strings.Contains(text, "an equally distinctive cause string") {
		t.Fatalf("pm_context leaked journal CONTENT:\n%s", text)
	}
}

// A project with no journals declared must not gain a journals key at all -
// pm_context's payload is charged to every session.
func TestE2EContextOmitsJournalsWhenNoneDeclared(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}
	if strings.Contains(text, "journals") {
		t.Fatalf("journals key present with none declared:\n%s", text)
	}
}
