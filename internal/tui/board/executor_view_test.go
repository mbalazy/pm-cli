package board

import (
	"encoding/json"
	"fmt"
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
	// Real transcripts (jsonl) terminate every line, including the last, with
	// \n - an unterminated trailing line is what an in-progress write looks
	// like, and transcriptCache deliberately holds it back until it completes.
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
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

func assistantLine(text string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, text)
}

// TestTranscriptCacheSkipsUnchangedFile proves a tick on an unchanged transcript
// never re-reads/re-decodes it: it rewrites the file with DIFFERENT content but
// fakes back the original size+mtime, then checks render() still returns the
// STALE cached output - if it had re-decoded, it would see the new content.
func TestTranscriptCacheSkipsUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	line1 := assistantLine("first") + "\n"
	if err := os.WriteFile(path, []byte(line1), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime()

	c := &transcriptCache{}
	out1 := stripANSI(c.render(path, 100, false))
	if !strings.Contains(out1, "first") {
		t.Fatalf("initial decode missing %q, got %q", "first", out1)
	}

	// Same byte length as "first" so the file size doesn't change.
	line2 := assistantLine("TAMPR") + "\n"
	if len(line2) != len(line1) {
		t.Fatalf("test fixture bug: line lengths differ (%d vs %d)", len(line1), len(line2))
	}
	if err := os.WriteFile(path, []byte(line2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	out2 := stripANSI(c.render(path, 100, false))
	if out2 != out1 {
		t.Errorf("render() re-decoded a file with unchanged size+mtime: got %q, want cached %q", out2, out1)
	}
	if strings.Contains(out2, "TAMPR") {
		t.Error("render() picked up content from a file whose size+mtime didn't change - it must have re-read the file")
	}
}

// TestTranscriptCacheAppendIsIncremental proves an append only decodes the NEW
// bytes: after the first render, it tampers the ALREADY-RENDERED prefix bytes
// in place (same length, so the append still lands after it) and appends a new
// line. A correct incremental reader seeks past the tampered prefix and never
// re-parses it, so the tampered marker must not appear in the output.
func TestTranscriptCacheAppendIsIncremental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	line1 := assistantLine("first")
	if err := os.WriteFile(path, []byte(line1+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := &transcriptCache{}
	out1 := stripANSI(c.render(path, 100, false))
	if !strings.Contains(out1, "first") {
		t.Fatalf("initial decode missing %q, got %q", "first", out1)
	}

	// Tamper the already-consumed prefix in place (same byte length) and append
	// a second line. A full re-parse would surface "TAMPR"; an incremental one
	// (seeking to the previous EOF) must not.
	tampered := strings.Replace(line1, "first", "TAMPR", 1)
	if len(tampered) != len(line1) {
		t.Fatalf("test fixture bug: tampered line length differs")
	}
	full := tampered + "\n" + assistantLine("second") + "\n"
	if err := os.WriteFile(path, []byte(full), 0644); err != nil {
		t.Fatal(err)
	}

	out2 := stripANSI(c.render(path, 100, false))
	if !strings.Contains(out2, "first") {
		t.Errorf("append render lost the original prefix: got %q", out2)
	}
	if !strings.Contains(out2, "second") {
		t.Errorf("append render missing the newly appended line: got %q", out2)
	}
	if strings.Contains(out2, "TAMPR") {
		t.Errorf("append render re-parsed the already-consumed prefix instead of just the new bytes: got %q", out2)
	}
}

// TestTranscriptCacheWidthChangeForcesFullDecode ensures a resize (width
// change) doesn't keep serving a cache rendered at the old width.
func TestTranscriptCacheWidthChangeForcesFullDecode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(assistantLine("hello")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := &transcriptCache{}
	_ = c.render(path, 100, false)
	out := stripANSI(c.render(path, 40, false))
	if !strings.Contains(out, "hello") {
		t.Errorf("re-decode at new width lost content: got %q", out)
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

// bigTranscript writes n synthetic assistant lines to path, simulating a
// long-running worker's transcript.
func bigTranscript(t *testing.B, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(assistantLine(fmt.Sprintf("step %d doing some work with a reasonably long line of text to pad it out", i)))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkDecodeTranscriptFull is the pre-fix baseline: a full read + decode
// of the whole transcript, as happened on every 2s tick.
func BenchmarkDecodeTranscriptFull(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	bigTranscript(b, path, 5000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeTranscript(path, 100, false)
	}
}

// BenchmarkTranscriptCacheUnchanged shows the fix: a tick against an unchanged
// large transcript costs near nothing (size+mtime skip), vs. the full re-decode
// above scaling with file size.
func BenchmarkTranscriptCacheUnchanged(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	bigTranscript(b, path, 5000)

	c := &transcriptCache{}
	c.render(path, 100, false) // prime the cache

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.render(path, 100, false)
	}
}

// BenchmarkTranscriptCacheAppend shows an incremental append against a large,
// already-cached transcript costs roughly the same regardless of how large the
// existing prefix is (it only reads/parses the newly appended line), vs. the
// full re-decode's cost growing linearly with total file size.
func BenchmarkTranscriptCacheAppend(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	bigTranscript(b, path, 5000)

	c := &transcriptCache{}
	c.render(path, 100, false) // prime the cache with the initial 5000 lines

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.WriteString(assistantLine(fmt.Sprintf("appended %d", i)) + "\n"); err != nil {
			b.Fatal(err)
		}
		c.render(path, 100, false)
	}
}
