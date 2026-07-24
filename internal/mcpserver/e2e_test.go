package mcpserver

import (
	"context"
	"encoding/json"
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

	// delete
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
