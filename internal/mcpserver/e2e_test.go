package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startMCP wires the REAL registered tool handlers to an in-memory MCP
// client session, so tests exercise the exact code Claude Code calls
// (input decoding, handler logic, JSON rendering) - not a re-simulation.
func startMCP(t *testing.T, store storage.TaskStore) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "pm-test", Version: "test"}, nil)
	registerTools(s, store)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _ = s.Run(ctx, serverT) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	sess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// call invokes a tool and returns (text payload, isError).
func call(t *testing.T, sess *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func mustUnmarshal(t *testing.T, text string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
}

func TestE2EAddGetUpdateMoveDelete(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	// add with spec + brief + links
	text, isErr := call(t, sess, "pm_add_task", map[string]any{
		"project": "test", "title": "New feature",
		"spec":  "## Description\nBuild X",
		"brief": "cold start ctx",
		"links": map[string]string{"jira": "SCRUM-9"},
		"tags":  []string{"feat"},
	})
	if isErr {
		t.Fatalf("add_task error: %s", text)
	}
	var added struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	mustUnmarshal(t, text, &added)
	if added.ID == "" || !strings.Contains(added.Body, "spec:start") {
		t.Fatalf("add result: %+v", added)
	}

	// get
	text, isErr = call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": added.ID})
	if isErr {
		t.Fatalf("get_task error: %s", text)
	}

	// update: merge a link, append body, overwrite brief
	text, isErr = call(t, sess, "pm_update_task", map[string]any{
		"project": "test", "task_id": added.ID,
		"links":       map[string]string{"pr": "https://x/pr/1"},
		"body_append": "Session 1: note",
		"brief":       "new brief",
	})
	if isErr {
		t.Fatalf("update_task error: %s", text)
	}
	final, err := store.FindTask("test", added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Meta.Links["jira"] != "SCRUM-9" || final.Meta.Links["pr"] != "https://x/pr/1" {
		t.Fatalf("links must merge, got %v", final.Meta.Links)
	}
	if !strings.Contains(final.Body, "Build X") || !strings.Contains(final.Body, "Session 1: note") {
		t.Fatalf("body: %q", final.Body)
	}
	if final.Meta.Brief != "new brief" {
		t.Fatalf("brief: %q", final.Meta.Brief)
	}

	// move to done
	_, isErr = call(t, sess, "pm_move_task", map[string]any{"project": "test", "task_id": added.ID, "new_status": "done"})
	if isErr {
		t.Fatal("move_task error")
	}
	moved, _ := store.FindTask("test", added.ID)
	if moved.Meta.Status != storage.StatusDone {
		t.Fatalf("status = %s", moved.Meta.Status)
	}

	// delete: a fuzzy/title query must NOT delete anything
	text, isErr = call(t, sess, "pm_delete_task", map[string]any{"project": "test", "task_id": "New feature"})
	if !isErr {
		t.Fatalf("delete_task with fuzzy title query should error, got: %s", text)
	}
	if _, err := store.FindTask("test", added.ID); err != nil {
		t.Fatalf("task must survive a fuzzy delete query, got: %v", err)
	}

	// delete: exact ID removes the task
	_, isErr = call(t, sess, "pm_delete_task", map[string]any{"project": "test", "task_id": added.ID})
	if isErr {
		t.Fatal("delete_task error")
	}
	if _, err := store.FindTask("test", added.ID); err == nil {
		t.Fatal("task still exists after delete")
	}
}

func TestE2EErrorsAreToolErrors(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": "t-404"})
	if !isErr {
		t.Fatalf("missing task must be a tool error, got: %s", text)
	}
	text, isErr = call(t, sess, "pm_update_task", map[string]any{"project": "nope", "task_id": "t-1"})
	if !isErr {
		t.Fatalf("unknown project must be a tool error, got: %s", text)
	}
}

// TestE2EInvalidStatusRejected: a status outside the project's set renders on
// no board column (the task vanishes), so both mutation paths must reject it -
// while system-level "archived" always passes.
func TestE2EInvalidStatusRejected(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_move_task", map[string]any{"project": "test", "task_id": "t-1", "new_status": "dnoe"})
	if !isErr {
		t.Fatalf("pm_move_task with a typo'd status must be a tool error, got: %s", text)
	}
	text, isErr = call(t, sess, "pm_update_task", map[string]any{"project": "test", "task_id": "t-1", "status": "bogus"})
	if !isErr {
		t.Fatalf("pm_update_task with an unknown status must be a tool error, got: %s", text)
	}
	after, err := store.FindTask("test", "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Meta.Status != storage.StatusDoing {
		t.Fatalf("rejected writes must not change the status, got %s", after.Meta.Status)
	}

	if text, isErr = call(t, sess, "pm_move_task", map[string]any{"project": "test", "task_id": "t-1", "new_status": "archived"}); isErr {
		t.Fatalf("archived must always be movable to: %s", text)
	}
}

func TestE2EContextAndListTasks(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}
	if !strings.Contains(text, "t-1") || !strings.Contains(text, "initial brief") {
		t.Fatalf("context must include the doing task + brief: %s", text)
	}

	text, isErr = call(t, sess, "pm_list_tasks", map[string]any{"project": "test", "status": "doing"})
	if isErr {
		t.Fatalf("list_tasks error: %s", text)
	}
	if !strings.Contains(text, "t-1") {
		t.Fatalf("list must include t-1: %s", text)
	}
}

// TestE2EConcurrentUpdatesDontDropWrites drives the REAL handler path from
// many goroutines: every body_append must survive (guarded by LockProject).
func TestE2EConcurrentUpdatesDontDropWrites(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	const writers = 6
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, isErr := call(t, sess, "pm_update_task", map[string]any{
				"project": "test", "task_id": "t-1",
				"body_append": "entry-" + string(rune('A'+i)),
			})
			if isErr {
				t.Error("update error")
			}
		}(i)
	}
	wg.Wait()

	final, err := store.FindTask("test", "t-1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < writers; i++ {
		want := "entry-" + string(rune('A'+i))
		if !strings.Contains(final.Body, want) {
			t.Fatalf("lost update: %s missing from body %q", want, final.Body)
		}
	}
}

// TestE2EListTasksBudget covers the pm_list_tasks output budget: default cap
// of 50 newest tasks, explicit total/shown + note on truncation, a working
// limit param, and one-line briefs in listings.
func TestE2EListTasksBudget(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	projDir := filepath.Join(store.RootDir(), "test")
	for i := 2; i <= 60; i++ {
		storage.WriteTask(&storage.Task{
			Meta: storage.TaskMeta{
				ID:      fmt.Sprintf("t-%d", i),
				Title:   fmt.Sprintf("Task %d", i),
				Status:  storage.StatusTodo,
				Created: "2025-01-01",
				// Unique, lexicographically increasing "dates": Updated is
				// compared as a plain string, and t-60 must be the newest.
				Updated: fmt.Sprintf("2025-03-01-%03d", i),
				Brief:   "line one\nline two",
			},
			FilePath: filepath.Join(projDir, fmt.Sprintf("t-%d.md", i)),
			Project:  "test",
		})
	}
	sess := startMCP(t, store)

	type listOut struct {
		Tasks []struct {
			ID    string `json:"id"`
			Brief string `json:"brief"`
		} `json:"tasks"`
		Total int    `json:"total"`
		Shown int    `json:"shown"`
		Note  string `json:"note"`
	}

	t.Run("default limit truncates with note", func(t *testing.T) {
		text, isErr := call(t, sess, "pm_list_tasks", map[string]any{"project": "test"})
		if isErr {
			t.Fatalf("list error: %s", text)
		}
		var out listOut
		mustUnmarshal(t, text, &out)
		if out.Total != 60 || out.Shown != 50 || len(out.Tasks) != 50 {
			t.Fatalf("total=%d shown=%d len=%d, want 60/50/50", out.Total, out.Shown, len(out.Tasks))
		}
		if !strings.Contains(out.Note, "50 of 60") {
			t.Fatalf("note should report truncation, got %q", out.Note)
		}
	})

	t.Run("briefs are one line", func(t *testing.T) {
		text, _ := call(t, sess, "pm_list_tasks", map[string]any{"project": "test"})
		var out listOut
		mustUnmarshal(t, text, &out)
		for _, task := range out.Tasks {
			if strings.Contains(task.Brief, "\n") {
				t.Fatalf("brief of %s not one-line: %q", task.ID, task.Brief)
			}
			if task.Brief == "line one\nline two" {
				t.Fatalf("brief of %s not compressed", task.ID)
			}
		}
	})

	t.Run("limit param wins over default", func(t *testing.T) {
		text, _ := call(t, sess, "pm_list_tasks", map[string]any{"project": "test", "limit": 5})
		var out listOut
		mustUnmarshal(t, text, &out)
		if out.Shown != 5 || out.Total != 60 {
			t.Fatalf("shown=%d total=%d, want 5/60", out.Shown, out.Total)
		}
	})

	t.Run("limit above total shows everything without note", func(t *testing.T) {
		text, _ := call(t, sess, "pm_list_tasks", map[string]any{"project": "test", "limit": 100})
		var out listOut
		mustUnmarshal(t, text, &out)
		if out.Shown != 60 || out.Note != "" {
			t.Fatalf("shown=%d note=%q, want 60 and empty note", out.Shown, out.Note)
		}
	})

	t.Run("newest first survives truncation", func(t *testing.T) {
		text, _ := call(t, sess, "pm_list_tasks", map[string]any{"project": "test", "limit": 1})
		var out listOut
		mustUnmarshal(t, text, &out)
		if len(out.Tasks) != 1 || out.Tasks[0].ID != "t-60" {
			t.Fatalf("tasks[0]=%v, want the newest (t-60)", out.Tasks)
		}
	})
}

// TestE2EContextBodyCapped: pm_context returns doing tasks with the body
// capped at contextBodyLimit + a pointer to pm_get_task; the brief stays full.
func TestE2EContextBodyCapped(t *testing.T) {
	store, task := setupMCPTestStore(t)
	task.Body = strings.Repeat("x", 10000)
	storage.WriteTask(task)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}
	var out struct {
		DoingTasks []struct {
			Body  string `json:"body"`
			Brief string `json:"brief"`
		} `json:"doing_tasks"`
	}
	mustUnmarshal(t, text, &out)
	if len(out.DoingTasks) != 1 {
		t.Fatalf("want 1 doing task, got %d", len(out.DoingTasks))
	}
	body := out.DoingTasks[0].Body
	if len(body) > contextBodyLimit+100 {
		t.Fatalf("body not capped: %d chars", len(body))
	}
	if !strings.Contains(body, "pm_get_task") {
		t.Fatalf("capped body must point to pm_get_task, got tail %q", body[len(body)-80:])
	}
	if out.DoingTasks[0].Brief != "initial brief" {
		t.Fatalf("brief must stay full in pm_context, got %q", out.DoingTasks[0].Brief)
	}

	// A short body passes through untouched.
	task.Body = "short body"
	storage.WriteTask(task)
	text, _ = call(t, sess, "pm_context", map[string]any{"project": "test"})
	mustUnmarshal(t, text, &out)
	if out.DoingTasks[0].Body != "short body" {
		t.Fatalf("short body must be untouched, got %q", out.DoingTasks[0].Body)
	}
}

// execFields is the read side of the executor frontmatter: pm_get_task must
// surface model + epic_mode (and the pre-existing mode) so a prep skill can
// see what it wrote.
type execFields struct {
	ID       string `json:"id"`
	Mode     string `json:"mode"`
	Model    string `json:"model"`
	EpicMode string `json:"epic_mode"`
}

func getExecFields(t *testing.T, sess *mcp.ClientSession, id string) execFields {
	t.Helper()
	text, isErr := call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": id})
	if isErr {
		t.Fatalf("get_task error: %s", text)
	}
	var got execFields
	mustUnmarshal(t, text, &got)
	return got
}

// TestE2EEpicModeAndModel drives the real handlers for the executor fields:
// epic_mode is settable via add + update (with ValidateEpicMode at write) and
// both model and epic_mode come back out of pm_get_task.
func TestE2EEpicModeAndModel(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	t.Run("add sets epic_mode and model, get returns both", func(t *testing.T) {
		text, isErr := call(t, sess, "pm_add_task", map[string]any{
			"project": "test", "title": "Batch tracker",
			"epic_mode": storage.EpicModeIndependent,
			"model":     "sonnet",
			"mode":      "manual",
		})
		if isErr {
			t.Fatalf("add_task error: %s", text)
		}
		var added execFields
		mustUnmarshal(t, text, &added)

		// on disk (the add handler's own result is not the only witness)
		onDisk, err := store.FindTask("test", added.ID)
		if err != nil {
			t.Fatal(err)
		}
		if onDisk.Meta.EpicMode != storage.EpicModeIndependent {
			t.Errorf("on-disk epic_mode = %q, want %q", onDisk.Meta.EpicMode, storage.EpicModeIndependent)
		}
		if onDisk.Meta.Model != "sonnet" {
			t.Errorf("on-disk model = %q, want sonnet", onDisk.Meta.Model)
		}

		got := getExecFields(t, sess, added.ID)
		if got.EpicMode != storage.EpicModeIndependent {
			t.Errorf("get epic_mode = %q, want %q", got.EpicMode, storage.EpicModeIndependent)
		}
		if got.Model != "sonnet" {
			t.Errorf("get model = %q, want sonnet", got.Model)
		}
		if got.Mode != "manual" {
			t.Errorf("get mode = %q, want manual", got.Mode)
		}
	})

	t.Run("update sets, keeps on omit, clears on empty string", func(t *testing.T) {
		text, isErr := call(t, sess, "pm_add_task", map[string]any{
			"project": "test", "title": "Plain tracker",
		})
		if isErr {
			t.Fatalf("add_task error: %s", text)
		}
		var added execFields
		mustUnmarshal(t, text, &added)
		if added.EpicMode != "" {
			t.Fatalf("fresh task epic_mode = %q, want empty", added.EpicMode)
		}

		// set
		if _, isErr := call(t, sess, "pm_update_task", map[string]any{
			"project": "test", "task_id": added.ID,
			"epic_mode": storage.EpicModeIndependent,
			"model":     "haiku",
		}); isErr {
			t.Fatal("update_task error")
		}
		if got := getExecFields(t, sess, added.ID); got.EpicMode != storage.EpicModeIndependent {
			t.Fatalf("after set, epic_mode = %q", got.EpicMode)
		}

		// omit = unchanged (an unrelated update must not clear it)
		if _, isErr := call(t, sess, "pm_update_task", map[string]any{
			"project": "test", "task_id": added.ID, "brief": "still independent",
		}); isErr {
			t.Fatal("update_task error")
		}
		got := getExecFields(t, sess, added.ID)
		if got.EpicMode != storage.EpicModeIndependent {
			t.Fatalf("omitted epic_mode must be kept, got %q", got.EpicMode)
		}
		if got.Model != "haiku" {
			t.Fatalf("omitted model must be kept, got %q", got.Model)
		}

		// empty string = clear (back to integration mode)
		if _, isErr := call(t, sess, "pm_update_task", map[string]any{
			"project": "test", "task_id": added.ID, "epic_mode": "",
		}); isErr {
			t.Fatal("update_task error")
		}
		if got := getExecFields(t, sess, added.ID); got.EpicMode != "" {
			t.Fatalf("empty epic_mode must clear, got %q", got.EpicMode)
		}
	})

	t.Run("invalid epic_mode rejected on add", func(t *testing.T) {
		before, err := store.GetTasks("test")
		if err != nil {
			t.Fatal(err)
		}
		text, isErr := call(t, sess, "pm_add_task", map[string]any{
			"project": "test", "title": "Bad tracker", "epic_mode": "INDEPENDENT",
		})
		if !isErr {
			t.Fatalf("invalid epic_mode must be a tool error, got: %s", text)
		}
		// Pin the message to ValidateEpicMode's own wording: a bare "epic_mode"
		// substring would also match an SDK-level schema/decode rejection, so the
		// assertion would survive the validator being dropped.
		if !strings.Contains(text, "invalid epic_mode") {
			t.Errorf("error must come from ValidateEpicMode, got: %s", text)
		}
		after, err := store.GetTasks("test")
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Errorf("rejected add must not create a task (%d -> %d)", len(before), len(after))
		}
	})

	t.Run("invalid epic_mode rejected on update, whole call rolls back", func(t *testing.T) {
		before, err := store.FindTask("test", "t-1")
		if err != nil {
			t.Fatal(err)
		}
		// Co-pass title: the handler assigns it into the in-memory task BEFORE
		// it reaches the epic_mode check, so this is the field that would leak
		// if the rejected call ever made it to a write.
		text, isErr := call(t, sess, "pm_update_task", map[string]any{
			"project": "test", "task_id": "t-1",
			"epic_mode": "batch", "title": "should not be written",
		})
		if !isErr {
			t.Fatalf("invalid epic_mode must be a tool error, got: %s", text)
		}
		if !strings.Contains(text, "invalid epic_mode") {
			t.Errorf("error must come from ValidateEpicMode, got: %s", text)
		}
		reloaded, err := store.FindTask("test", "t-1")
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Meta.EpicMode != "" {
			t.Errorf("on-disk epic_mode = %q, want empty (write must not go through)", reloaded.Meta.EpicMode)
		}
		if reloaded.Meta.Title != before.Meta.Title {
			t.Errorf("co-passed title was persisted by a rejected update: %q -> %q", before.Meta.Title, reloaded.Meta.Title)
		}
	})
}

// writeTaskFor is a terse task writer for the payload-shape tests.
func writeTaskFor(t *testing.T, store *storage.Store, id, parent string, status storage.TaskStatus, brief string) {
	t.Helper()
	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID: id, Title: "Task " + id, Status: status, Parent: parent,
			Created: "2025-01-01", Updated: "2025-01-02", Brief: brief,
		},
		Body:     strings.Repeat("body ", 200),
		FilePath: filepath.Join(store.Root, "test", id+".md"),
		Project:  "test",
	}
	if err := storage.WriteTask(task); err != nil {
		t.Fatalf("write %s: %v", id, err)
	}
}

// TestE2EContextRollupIsCompressed pins the pm_context size contract (pm-cli-50:
// the MCP payload hit 61k chars and blew the tool's token limit while the same
// rollup from the terminal was 15k). The rollup is a summary: every brief in it
// is one line and a finished tracker drops its children. Full briefs stay one
// pm_get_task away.
func TestE2EContextRollupIsCompressed(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	fatBrief := "**headline of the brief**\n" + strings.Repeat("padding line that must never reach pm_context\n", 60)

	// An open tracker: children stay, briefs compress.
	writeTaskFor(t, store, "t-10", "", storage.StatusDoing, fatBrief)
	for i := 1; i <= 3; i++ {
		writeTaskFor(t, store, fmt.Sprintf("t-10-%d", i), "t-10", storage.StatusTodo, fatBrief)
	}
	// A finished tracker: children collapse entirely.
	writeTaskFor(t, store, "t-20", "", storage.StatusDone, fatBrief)
	for i := 1; i <= 4; i++ {
		writeTaskFor(t, store, fmt.Sprintf("t-20-%d", i), "t-20", storage.StatusDone, fatBrief)
	}

	// A focus plan pointing at a fat-brief task.
	storage.WriteFocusPlan(store.Root, storage.FocusPlan{Date: storage.Today(), Tasks: []string{"t-10"}})

	sess := startMCP(t, store)
	text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}

	var out struct {
		Trackers []struct {
			ID              string `json:"id"`
			BriefLine       string `json:"brief_line"`
			Total           int    `json:"total"`
			ChildrenOmitted bool   `json:"children_omitted"`
			Children        []struct {
				ID        string `json:"id"`
				BriefLine string `json:"brief_line"`
			} `json:"children"`
		} `json:"trackers"`
		FocusTasks []struct {
			ID    string `json:"id"`
			Brief string `json:"brief"`
		} `json:"focus_tasks"`
	}
	mustUnmarshal(t, text, &out)

	byID := map[string]int{}
	for i, tr := range out.Trackers {
		byID[tr.ID] = i
		if strings.Contains(tr.BriefLine, "\n") {
			t.Errorf("tracker %s brief_line is multi-line: %q", tr.ID, tr.BriefLine)
		}
		if tr.BriefLine != "headline of the brief" {
			t.Errorf("tracker %s brief_line = %q, want the compressed headline", tr.ID, tr.BriefLine)
		}
		for _, c := range tr.Children {
			if strings.Contains(c.BriefLine, "\n") {
				t.Errorf("child %s brief_line is multi-line: %q", c.ID, c.BriefLine)
			}
		}
	}

	open := out.Trackers[byID["t-10"]]
	if len(open.Children) != 3 || open.ChildrenOmitted {
		t.Errorf("open tracker: children=%d omitted=%v, want 3 and false", len(open.Children), open.ChildrenOmitted)
	}
	done := out.Trackers[byID["t-20"]]
	if len(done.Children) != 0 || !done.ChildrenOmitted {
		t.Errorf("finished tracker: children=%d omitted=%v, want 0 and true", len(done.Children), done.ChildrenOmitted)
	}
	if done.Total != 4 {
		t.Errorf("finished tracker Total = %d, want 4 (progress survives the collapse)", done.Total)
	}

	if len(out.FocusTasks) != 1 || strings.Contains(out.FocusTasks[0].Brief, "\n") {
		t.Errorf("focus task brief must be one line, got %q", out.FocusTasks[0].Brief)
	}

	// The padding text exists on 9 tasks; not one line of it may reach the rollup.
	if n := strings.Count(text, "padding line"); n != 0 {
		t.Errorf("payload leaks %d padding lines - a full brief reached pm_context", n)
	}

	// pm_get_task is the escape hatch: it still returns the whole brief.
	full, isErr := call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": "t-10"})
	if isErr {
		t.Fatalf("pm_get_task error: %s", full)
	}
	var detail struct {
		Brief string `json:"brief"`
	}
	mustUnmarshal(t, full, &detail)
	if detail.Brief != fatBrief {
		t.Errorf("pm_get_task must return the full brief, got %d of %d chars", len(detail.Brief), len(fatBrief))
	}
}

// TestE2ECrossProjectContextCompressesBriefs: the no-project branch of
// pm_context spans every active project, so its doing-task briefs compress like
// any other listing (the project-scoped branch keeps them whole on purpose).
func TestE2ECrossProjectContextCompressesBriefs(t *testing.T) {
	store, task := setupMCPTestStore(t)
	task.Meta.Brief = "headline\nrest of the brief"
	storage.WriteTask(task)

	sess := startMCP(t, store)
	text, isErr := call(t, sess, "pm_context", nil)
	if isErr {
		t.Fatalf("pm_context error: %s", text)
	}
	var out struct {
		Projects []struct {
			DoingTasks []struct {
				Brief string `json:"brief"`
			} `json:"doing_tasks"`
		} `json:"projects"`
	}
	mustUnmarshal(t, text, &out)
	if len(out.Projects) != 1 || len(out.Projects[0].DoingTasks) != 1 {
		t.Fatalf("want 1 project with 1 doing task, got %+v", out.Projects)
	}
	if got := out.Projects[0].DoingTasks[0].Brief; got != "headline" {
		t.Errorf("cross-project brief = %q, want the compressed first line", got)
	}
}
