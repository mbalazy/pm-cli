package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// spendLimitLine is the message Claude Code actually emitted in all four
// observed deaths, copied verbatim from the transcripts.
const spendLimitLine = "You've hit your monthly spend limit · raise it at claude.ai/settings/usage?from=cc_cli_limit_message"

// assistantText builds one transcript record the way a real transcript carries
// an assistant message.
func assistantText(text string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, text)
}

func TestScanLastAssistantText(t *testing.T) {
	t.Run("returns the LAST assistant message", func(t *testing.T) {
		tr := strings.Join([]string{
			assistantText("first, I will read the file"),
			`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"not mine"}]}}`,
			assistantText(spendLimitLine),
			`{"type":"last-prompt"}`,
		}, "\n")
		got := scanLastAssistantText(strings.NewReader(tr))
		if got != spendLimitLine {
			t.Errorf("got %q, want the final assistant line", got)
		}
	})

	t.Run("skips tool_use blocks - a tool call says nothing readable", func(t *testing.T) {
		tr := strings.Join([]string{
			assistantText("running the gate"),
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`,
		}, "\n")
		if got := scanLastAssistantText(strings.NewReader(tr)); got != "running the gate" {
			t.Errorf("got %q, want the last message that had text", got)
		}
	})

	t.Run("a truncated final line does not lose the rest", func(t *testing.T) {
		// A killed worker can leave the transcript cut mid-write.
		tr := assistantText("the useful part") + "\n" + `{"type":"assist`
		if got := scanLastAssistantText(strings.NewReader(tr)); got != "the useful part" {
			t.Errorf("got %q - an unparsable line must be skipped, not fatal", got)
		}
	})

	t.Run("nothing to say", func(t *testing.T) {
		if got := scanLastAssistantText(strings.NewReader("")); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestAccountWallReason(t *testing.T) {
	walls := []string{
		spendLimitLine,
		"Credit balance is too low to continue",
		"Usage limit reached · resets at 5pm",
		"Please run /login to authenticate",
		"Invalid API key · check your credentials",
	}
	for _, w := range walls {
		if accountWallReason(w) == "" {
			t.Errorf("not recognised as an account wall: %q", w)
		}
	}

	// The damaging direction is a FALSE POSITIVE: it would stop a healthy run.
	// Ordinary worker prose about its own task must never trip it.
	notWalls := []string{
		"I hit the max-turns limit while refactoring",
		"the test asserts the rate limiter rejects the 11th request",
		"blocked: the API contract is unclear",
		"added a spend report to the billing screen",
		"",
	}
	for _, s := range notWalls {
		if r := accountWallReason(s); r != "" {
			t.Errorf("false positive on %q -> %q", s, r)
		}
	}
}

func TestTailWriterKeepsTheEnd(t *testing.T) {
	w := &tailWriter{n: 10}
	fmt.Fprint(w, "0123456789ABCDE")
	// 15 bytes in, the last 10 kept.
	if got := w.String(); got != "56789ABCDE" {
		t.Errorf("got %q, want the trailing bytes", got)
	}
}

// TestWorkerDeathError is the headline of this change: a worker that dies
// without an envelope used to produce six words that named no cause, and the
// reason sat unread in a transcript pm already knew the path of.
func TestWorkerDeathError(t *testing.T) {
	exit1 := errors.New("exit status 1")
	dir := t.TempDir()

	t.Run("an ordinary death carries the worker's last message", func(t *testing.T) {
		cfg := t.TempDir()
		writeTranscript(t, cfg, dir, "sess-1", assistantText("I cannot find the file the spec names."))
		err := workerDeathError(cfg, dir, "sess-1", "", exit1)

		var wall *accountWallError
		if errors.As(err, &wall) {
			t.Fatal("an ordinary failure must not be classified as an account wall")
		}
		if !strings.Contains(err.Error(), "I cannot find the file") {
			t.Errorf("the note must carry what the worker said, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), "exit status 1") {
			t.Errorf("the exit status must survive, got %q", err.Error())
		}
	})

	t.Run("an account wall is marked as one", func(t *testing.T) {
		cfg := t.TempDir()
		writeTranscript(t, cfg, dir, "sess-2", assistantText(spendLimitLine))
		err := workerDeathError(cfg, dir, "sess-2", "", exit1)

		var wall *accountWallError
		if !errors.As(err, &wall) {
			t.Fatalf("the spend limit must be recognised, got %q", err.Error())
		}
		if !strings.Contains(wall.reason, "spend limit") {
			t.Errorf("the reason must name the wall, got %q", wall.reason)
		}
		if !strings.Contains(err.Error(), "monthly spend limit") {
			t.Errorf("the worker's own words must stay in the message, got %q", err.Error())
		}
		if !errors.Is(err, exit1) {
			t.Error("the underlying exit error must stay unwrappable")
		}
	})

	t.Run("no transcript: stderr is the fallback", func(t *testing.T) {
		err := workerDeathError(t.TempDir(), dir, "sess-none", "claude: command not found", exit1)
		if !strings.Contains(err.Error(), "command not found") {
			t.Errorf("a death before any transcript must still say something, got %q", err.Error())
		}
	})

	t.Run("nothing anywhere: the old bare message", func(t *testing.T) {
		err := workerDeathError(t.TempDir(), dir, "sess-none", "", exit1)
		if err.Error() != "claude worker failed: exit status 1" {
			t.Errorf("with nothing to add, the message must not gain noise: %q", err.Error())
		}
	})
}
