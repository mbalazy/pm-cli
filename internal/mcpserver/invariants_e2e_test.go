package mcpserver

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The task-field invariants agents rely on (CLAUDE.md, MCP "Invariants"),
// each proven through the real registered handlers and read back off the wire
// via pm_get_task - never by re-enacting the handler's logic in the test.

type wireTask struct {
	Status   string            `json:"status"`
	Links    map[string]string `json:"links"`
	Body     string            `json:"body"`
	Brief    string            `json:"brief"`
	AC       string            `json:"ac"`
	Sessions []string          `json:"sessions"`
}

func getWireTask(t *testing.T, sess *mcp.ClientSession, id string) wireTask {
	t.Helper()
	text, isErr := call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": id})
	if isErr {
		t.Fatalf("get_task %s: %s", id, text)
	}
	var w wireTask
	mustUnmarshal(t, text, &w)
	return w
}

func mustCall(t *testing.T, sess *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	text, isErr := call(t, sess, tool, args)
	if isErr {
		t.Fatalf("%s: %s", tool, text)
	}
	return text
}

func update(t *testing.T, sess *mcp.ClientSession, args map[string]any) {
	t.Helper()
	args["project"] = "test"
	if _, ok := args["task_id"]; !ok {
		args["task_id"] = "t-1"
	}
	mustCall(t, sess, "pm_update_task", args)
}

// addBareTask adds a task with no links, body or sessions and returns its id.
func addBareTask(t *testing.T, sess *mcp.ClientSession) string {
	t.Helper()
	var added struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, mustCall(t, sess, "pm_add_task", map[string]any{"project": "test", "title": "Bare"}), &added)
	return added.ID
}

func TestE2ELinksMerge(t *testing.T) {
	t.Run("a new key is added next to the existing ones", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"links": map[string]string{"pr": "https://github.com/org/repo/pull/1"}})
		got := getWireTask(t, sess, "t-1").Links
		if got["jira"] != "SCRUM-1" || got["pr"] != "https://github.com/org/repo/pull/1" {
			t.Errorf("links = %v", got)
		}
	})

	t.Run("an existing key is updated", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"links": map[string]string{"jira": "SCRUM-2"}})
		if got := getWireTask(t, sess, "t-1").Links; got["jira"] != "SCRUM-2" || len(got) != 1 {
			t.Errorf("links = %v", got)
		}
	})

	t.Run("an update without links keeps them", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"title": "Renamed"})
		if got := getWireTask(t, sess, "t-1").Links; got["jira"] != "SCRUM-1" || len(got) != 1 {
			t.Errorf("links = %v", got)
		}
	})

	t.Run("a task with no links gets one", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		id := addBareTask(t, sess)
		update(t, sess, map[string]any{"task_id": id, "links": map[string]string{"pr": "https://github.com/org/repo/pull/2"}})
		if got := getWireTask(t, sess, id).Links; got["pr"] != "https://github.com/org/repo/pull/2" || len(got) != 1 {
			t.Errorf("links = %v", got)
		}
	})
}

func TestE2EBodyAppendOnlyAppends(t *testing.T) {
	t.Run("appended after the existing body", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"body_append": "## Notes\n\nNew note"})
		if got := getWireTask(t, sess, "t-1").Body; got != "## Description\n\nOriginal body\n\n## Notes\n\nNew note" {
			t.Errorf("body = %q", got)
		}
	})

	t.Run("an empty body becomes the appended text", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		id := addBareTask(t, sess)
		update(t, sess, map[string]any{"task_id": id, "body_append": "First content"})
		if got := getWireTask(t, sess, id).Body; got != "First content" {
			t.Errorf("body = %q", got)
		}
	})

	t.Run("an update without body_append keeps the body", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"brief": "something else"})
		if got := getWireTask(t, sess, "t-1").Body; got != "## Description\n\nOriginal body" {
			t.Errorf("body = %q", got)
		}
	})
}

func TestE2ESpecReplacesAndLogAppends(t *testing.T) {
	t.Run("the first spec is prepended and the existing body becomes the Log", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"spec": "We build X. Decision: use Y."})
		body := getWireTask(t, sess, "t-1").Body
		if got := storage.ExtractSpec(body); got != "We build X. Decision: use Y." {
			t.Errorf("spec = %q", got)
		}
		if !strings.Contains(body, "## Description\n\nOriginal body") {
			t.Errorf("original body lost from the Log:\n%s", body)
		}
	})

	t.Run("a new spec replaces the old in place while body_append grows the Log", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		update(t, sess, map[string]any{"spec": "spec v1"})
		update(t, sess, map[string]any{"spec": "spec v2", "body_append": "Session 2: Q3 resolved -> see Spec."})
		body := getWireTask(t, sess, "t-1").Body
		if got := storage.ExtractSpec(body); got != "spec v2" {
			t.Errorf("spec = %q, want spec v2", got)
		}
		if strings.Contains(body, "spec v1") {
			t.Errorf("old spec not replaced:\n%s", body)
		}
		if !strings.Contains(body, "Session 2: Q3 resolved -> see Spec.") || !strings.Contains(body, "Original body") {
			t.Errorf("the Log lost or missed an entry:\n%s", body)
		}
		if c := strings.Count(body, storage.SpecStart); c != 1 {
			t.Errorf("want 1 spec block, got %d", c)
		}
	})
}

// Brief and ac overwrite (and survive an update that omits them - the
// tri-state "omit" half lives in TestE2EUpdateTaskTriState).
func TestE2EBriefAndACOverwrite(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)
	update(t, sess, map[string]any{"brief": "new brief", "ac": "new acceptance criteria"})
	w := getWireTask(t, sess, "t-1")
	if w.Brief != "new brief" || w.AC != "new acceptance criteria" {
		t.Errorf("brief = %q, ac = %q", w.Brief, w.AC)
	}
}

func TestE2ESessionsAppend(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	update(t, sess, map[string]any{"sessions": []string{"abc-123"}})
	if got := getWireTask(t, sess, "t-1").Sessions; len(got) != 1 || got[0] != "abc-123" {
		t.Fatalf("sessions = %v, want [abc-123]", got)
	}
	update(t, sess, map[string]any{"sessions": []string{"def-456"}})
	if got := getWireTask(t, sess, "t-1").Sessions; len(got) != 2 || got[0] != "abc-123" || got[1] != "def-456" {
		t.Fatalf("sessions = %v, want [abc-123 def-456]", got)
	}
	update(t, sess, map[string]any{"title": "no sessions in this update"})
	if got := getWireTask(t, sess, "t-1").Sessions; len(got) != 2 {
		t.Fatalf("an update without sessions must keep them, got %v", got)
	}

	var added struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, mustCall(t, sess, "pm_add_task", map[string]any{
		"project": "test", "title": "Task with sessions", "sessions": []string{"sess-1", "sess-2"},
	}), &added)
	if got := getWireTask(t, sess, added.ID).Sessions; len(got) != 2 || got[0] != "sess-1" || got[1] != "sess-2" {
		t.Errorf("add sessions = %v, want [sess-1 sess-2]", got)
	}
}

// A status change keeps brief and ac - they are the summary a closed or parked
// task is read back by.
func TestE2EMovePreservesBriefAndAC(t *testing.T) {
	for _, status := range []string{"done", "archived", "waiting"} {
		t.Run(status, func(t *testing.T) {
			store, _ := setupMCPTestStore(t)
			sess := startMCP(t, store)
			mustCall(t, sess, "pm_move_task", map[string]any{"project": "test", "task_id": "t-1", "new_status": status})
			w := getWireTask(t, sess, "t-1")
			if w.Status != status {
				t.Fatalf("status = %q, want %q", w.Status, status)
			}
			if w.Brief != "initial brief" || w.AC != "- Login works\n- Error shown on invalid creds" {
				t.Errorf("brief = %q, ac = %q, want both preserved", w.Brief, w.AC)
			}
		})
	}
}
