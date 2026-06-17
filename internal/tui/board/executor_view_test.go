package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestDecodeTranscript(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Implementing the relative-time util"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"file_path":"apps/expo/features/exectest/relativeTime.ts"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"File created successfully"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"yarn jest relativeTime\nsecond line"}}]}}`,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}

	out := stripANSI(decodeTranscript(path, 100, false))

	for _, want := range []string{
		"Implementing the relative-time util",
		"🔧 Write",
		"relativeTime.ts",
		"File created successfully",
		"🔧 Bash",
		"yarn jest relativeTime",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("decoded transcript missing %q\n---\n%s", want, out)
		}
	}
	// Compact: Bash command second line dropped (firstLine only).
	if strings.Contains(out, "second line") {
		t.Error("compact Bash summary should show only the first line of the command")
	}
	// init/system events produce no feed lines.
	if strings.Contains(out, "init") {
		t.Error("system/init events should not render")
	}

	// Verbose: the full Bash command (all lines) is shown.
	full := stripANSI(decodeTranscript(path, 100, true))
	if !strings.Contains(full, "second line") {
		t.Errorf("verbose mode should include the full multi-line Bash command\n---\n%s", full)
	}
}

func TestDecodeTranscriptMissingFile(t *testing.T) {
	if got := decodeTranscript(filepath.Join(t.TempDir(), "nope.jsonl"), 80, false); got != "" {
		t.Errorf("missing file should decode to empty, got %q", got)
	}
}

func TestRenderExecutorDashboard(t *testing.T) {
	run := &storage.RunState{
		TaskID: "p-1", Kind: "run-epic", Status: storage.RunStatusDone,
		Started: "2026-06-17T10:00:00Z", Updated: "2026-06-17T10:05:00Z",
		Subs: []storage.SubRun{
			{ID: "p-1-1", Status: "merged", Note: "built the thing"},
			{ID: "p-1-2", Status: "blocked", Note: "needs backend field"},
		},
	}
	out := stripANSI(renderExecutorDashboard(run, 100))
	for _, want := range []string{"Executor run", "✓ done", "p-1-1", "merged", "p-1-2", "blocked", "needs backend field", "press W to watch"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard missing %q\n---\n%s", want, out)
		}
	}
	if renderExecutorDashboard(nil, 100) != "" {
		t.Error("nil run should render empty")
	}
}

func TestToolSummary(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Write", map[string]any{"file_path": "a/b/c/d.ts"}, ".../c/d.ts"},
		{"Read", map[string]any{"file_path": "short.ts"}, "short.ts"},
		{"Bash", map[string]any{"command": "go test ./...\nignored"}, "go test ./..."},
		{"Grep", map[string]any{"pattern": "func main"}, "func main"},
		{"Task", map[string]any{"description": "review diff"}, "review diff"},
		{"TodoWrite", map[string]any{"todos": []any{1, 2}}, ""},
	}
	for _, tt := range tests {
		if got := toolSummary(tt.name, tt.input); got != tt.want {
			t.Errorf("toolSummary(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestParseBlocks(t *testing.T) {
	// array form
	arr := parseBlocks(json.RawMessage(`[{"type":"text","text":"hi"},{"type":"tool_use","name":"Read"}]`))
	if len(arr) != 2 || arr[0].Text != "hi" || arr[1].Name != "Read" {
		t.Errorf("array parse failed: %+v", arr)
	}
	// string form -> single text block
	s := parseBlocks(json.RawMessage(`"just a string"`))
	if len(s) != 1 || s[0].Type != "text" || s[0].Text != "just a string" {
		t.Errorf("string parse failed: %+v", s)
	}
	// empty
	if parseBlocks(nil) != nil {
		t.Error("nil content should parse to nil")
	}
}

func TestTruncateAndShortPath(t *testing.T) {
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("short", 50); got != "short" {
		t.Errorf("truncate should pass through short strings, got %q", got)
	}
	if got := shortPath("a/b/c/d.ts"); got != ".../c/d.ts" {
		t.Errorf("shortPath = %q", got)
	}
	if got := shortPath("only.ts"); got != "only.ts" {
		t.Errorf("shortPath short = %q", got)
	}
}
