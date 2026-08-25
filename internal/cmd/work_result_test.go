package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptLine renders one assistant record carrying a StructuredOutput call,
// the shape Claude Code writes to <config>/projects/<encoded cwd>/<sid>.jsonl.
func transcriptLine(t *testing.T, res map[string]any) string {
	t.Helper()
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "emitting the result"},
				map[string]any{"type": "tool_use", "name": "StructuredOutput", "input": res},
			},
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal transcript record: %v", err)
	}
	return string(b)
}

// writeTranscript plants a transcript for sessionID as if the worker had run in
// dir under the given claude config dir, and returns its path.
func writeTranscript(t *testing.T, configDir, dir, sessionID string, lines ...string) string {
	t.Helper()
	projDir := filepath.Join(configDir, "projects", encodeProjectPath(dir))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	path := filepath.Join(projDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// envelopeNoResult is the exact failure this recovery exists for: the worker
// finished and emitted its result, then said one more thing (a late background
// task notification), so the harness dropped structured_output and left the
// trailing sentence in result.
const envelopeNoResult = `{"type":"result","is_error":false,"result":"Also already retrieved - that's the executor verify gate that passed.","session_id":"sess-1","num_turns":41,"total_cost_usd":2.75}`

func TestParseWorkerOutputRecoversResultFromTranscript(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path := writeTranscript(t, cfg, dir, "sess-1", transcriptLine(t, map[string]any{
		"status":     "verified",
		"summary":    "moved the timezone row",
		"branch":     "me-login/ACME-1387",
		"commits":    []string{"25b47a31"},
		"unresolved": []string{"TODO: check on the simulator"},
	}))

	var log bytes.Buffer
	res, sid, err := parseWorkerOutput([]byte(envelopeNoResult), &log, cfg, dir, "")
	if err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	if sid != "sess-1" {
		t.Errorf("session id = %q, want sess-1", sid)
	}
	if res.Status != "verified" || res.Branch != "me-login/ACME-1387" {
		t.Errorf("recovered result = %+v", res)
	}
	if len(res.Commits) != 1 || res.Commits[0] != "25b47a31" {
		t.Errorf("commits = %v, want [25b47a31]", res.Commits)
	}
	if len(res.Unresolved) != 1 {
		t.Errorf("unresolved = %v, want one item", res.Unresolved)
	}
	// Turns and cost exist only in the envelope - a recovery that dropped them
	// would silently zero out every retro's effort/cost numbers.
	if res.Turns != 41 || res.CostUSD != 2.75 {
		t.Errorf("run stats not lifted: turns=%d cost=%v", res.Turns, res.CostUSD)
	}
	if !strings.Contains(log.String(), "recovered status") || !strings.Contains(log.String(), path) {
		t.Errorf("recovery must be announced with its source, got %q", log.String())
	}
}

func TestParseWorkerOutputRecoveryTakesLastCallAndNormalizesStatus(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeTranscript(t, cfg, dir, "sess-1",
		transcriptLine(t, map[string]any{"status": "failed", "summary": "gate red", "branch": "b"}),
		transcriptLine(t, map[string]any{"status": "merged", "summary": "gate green after fix", "branch": "b"}),
	)

	res, _, err := parseWorkerOutput([]byte(envelopeNoResult), nil, cfg, dir, "")
	if err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	// Last call wins (a revised verdict), and the legacy "merged" spelling is
	// folded exactly as the envelope path folds it.
	if res.Status != "verified" {
		t.Errorf("status = %q, want verified (last call, normalized)", res.Status)
	}
	if res.Summary != "gate green after fix" {
		t.Errorf("summary = %q, want the last call's", res.Summary)
	}
}

func TestParseWorkerOutputEnvelopeWinsOverTranscript(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeTranscript(t, cfg, dir, "sess-1", transcriptLine(t, map[string]any{"status": "failed", "summary": "stale", "branch": "b"}))

	env := `{"type":"result","is_error":false,"session_id":"sess-1","num_turns":3,"total_cost_usd":0.5,` +
		`"structured_output":{"status":"verified","summary":"authoritative","branch":"b","commits":[],"unresolved":[]}}`
	var log bytes.Buffer
	res, _, err := parseWorkerOutput([]byte(env), &log, cfg, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Summary != "authoritative" {
		t.Errorf("summary = %q - the envelope must win when it has a result", res.Summary)
	}
	if log.Len() != 0 {
		t.Errorf("no recovery happened, nothing to announce, got %q", log.String())
	}
}

func TestParseWorkerOutputNoTranscriptKeepsOriginalError(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	_, sid, err := parseWorkerOutput([]byte(envelopeNoResult), nil, cfg, dir, "")
	if err == nil || !strings.Contains(err.Error(), "no structured result") {
		t.Fatalf("expected the original error, got %v", err)
	}
	if sid != "sess-1" {
		t.Errorf("session id must survive the failure, got %q", sid)
	}
}

func TestParseWorkerOutputIgnoresStatuslessCall(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeTranscript(t, cfg, dir, "sess-1", transcriptLine(t, map[string]any{"summary": "no verdict here"}))

	if _, _, err := parseWorkerOutput([]byte(envelopeNoResult), nil, cfg, dir, ""); err == nil {
		t.Fatal("a call without a status carries no verdict and must not be recovered")
	}
}

func TestParseWorkerOutputFindsTranscriptUnderDefaultConfigDir(t *testing.T) {
	def := t.TempDir()
	pinned := t.TempDir() // project's claude_config_dir - transcript is NOT here
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", def)
	writeTranscript(t, def, dir, "sess-1", transcriptLine(t, map[string]any{"status": "verified", "summary": "s", "branch": "b"}))

	if _, _, err := parseWorkerOutput([]byte(envelopeNoResult), nil, pinned, dir, ""); err != nil {
		t.Fatalf("must fall back to the default config dir: %v", err)
	}
}

func TestScanStructuredOutputSurvivesHugeLines(t *testing.T) {
	// Worker transcripts embed whole file reads and command output, so records
	// run far past bufio.Scanner's 64KB default. Without the raised cap the
	// scanner stops at the first fat line and the recovery finds nothing.
	fat := map[string]any{
		"type":    "assistant",
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 300_000)}}},
	}
	b, err := json.Marshal(fat)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lines := string(b) + "\n" + transcriptLine(t, map[string]any{"status": "verified", "summary": "s", "branch": "b"}) + "\n"

	res := scanStructuredOutput(strings.NewReader(lines))
	if res == nil || res.Status != "verified" {
		t.Fatalf("result after a 300KB record not found: %+v", res)
	}
}

func TestWorkerAllowedToolsReachesUVManagedGates(t *testing.T) {
	// A uv-managed project keeps pytest/ruff/mypy inside .venv, so `uv run` is
	// the only way to run one test file instead of the whole Makefile gate.
	for _, pat := range []string{
		"Bash(uv sync:*)",
		"Bash(uv run pytest:*)",
		"Bash(uv run ruff:*)",
		"Bash(uv run mypy:*)",
		"Bash(uv run ty:*)",
		"Bash(uv run alembic:*)",
	} {
		if !strings.Contains(workerAllowedTools, pat) {
			t.Errorf("allowlist is missing %s", pat)
		}
	}
	// The envelope hole a blanket entry would open: the disallow patterns match
	// on leading tokens, so `uv run git push --force` and `uv run gh pr merge`
	// slip past every one of them, and the PreToolUse guard only reads whole
	// commands for hook bypasses.
	if strings.Contains(workerAllowedTools, "Bash(uv:*)") || strings.Contains(workerAllowedTools, "Bash(uv run:*)") {
		t.Error("a blanket uv entry re-opens `uv run git push --force` - name the subcommands instead")
	}
}

func TestWorkerDisallowedToolsBlocksHookBypass(t *testing.T) {
	// The near-miss this list exists for: with git allowed wholesale, the ONE
	// commit form the envelope permitted was the one the project forbids.
	// Each pattern below was confirmed to actually deny against a live claude
	// run - see the note on workerDisallowedTools.
	for _, pat := range []string{
		"Bash(git commit --no-verify:*)",
		"Bash(git commit -n:*)",
		"Bash(git push --no-verify:*)",
		"Bash(git -c *)",
		"Bash(git config core.hooksPath:*)",
		"Bash(git config --local core.hooksPath:*)",
	} {
		if !strings.Contains(workerDisallowedTools, pat) {
			t.Errorf("disallowed list is missing %s", pat)
		}
	}
	// The token-boundary trap: this spelling was measured NOT to match
	// `git -c core.hooksPath=/dev/null commit`, so it must not come back as the
	// thing we rely on.
	if strings.Contains(workerDisallowedTools, "Bash(git -c core.hooksPath:*)") {
		t.Error("Bash(git -c core.hooksPath:*) does not match a `core.hooksPath=value` token - use Bash(git -c *)")
	}
}
