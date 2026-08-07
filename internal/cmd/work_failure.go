package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// This file is about the ONE outcome the executor used to report as nothing at
// all: a worker that dies without producing an envelope. pm turned every such
// death into the same six words -
//
//	claude worker failed: exit status 1
//
// - and 4 of the 22 non-green subs in the first 19 runs died that way, all four
// on the same cause: the account's hard monthly spend limit. Diagnosing it took
// pulling session ids out of run-states and grepping transcripts by hand, and
// the manager, knowing nothing, spawned the next sub 2 seconds later into the
// same wall.
//
// Two things fix that, and they are separate: SAY what the worker said (any
// death), and RECOGNISE the deaths that will repeat for every remaining sub.

// workerDeathTailBytes bounds how much of a dead worker's last words is kept.
// Enough for a multi-line stack or an auth message, far short of a build log.
const workerDeathTailBytes = 4096

// tailWriter keeps only the last n bytes written to it. The worker's stderr is
// already streamed to the run log; this is a bounded copy for the failure note,
// so a worker that spews megabytes cannot grow the manager's memory.
//
// The mutex is load-bearing, not defensive. os/exec writes a non-*os.File
// Stderr from its own copying goroutine, and with WaitDelay set (see
// procWaitDelay) Wait can return while that goroutine is still being unwound -
// which is exactly the path this writer exists to report on, so the read and
// the write really can overlap.
type tailWriter struct {
	n   int
	mu  sync.Mutex
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.n {
		w.buf = w.buf[len(w.buf)-w.n:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.buf))
}

// workerLastWords reconstructs what a worker said before dying, best-effort and
// in order of usefulness:
//
//  1. the last assistant message in its TRANSCRIPT. This is where the useful
//     text actually is: all four spend-limit deaths ended with the limit message
//     as their final assistant record, while the manager's run log - which
//     receives the worker's stderr verbatim - held nothing at all between two
//     "launching headless worker" lines. pm can always find the transcript
//     because it MINTS the session id and pins it with `claude --session-id`
//     before the worker starts, so this works even when no envelope came back.
//  2. failing that, the tail of the worker's stderr, which covers the deaths
//     that happen before a transcript exists (a missing binary, a rejected flag).
//
// Empty when neither has anything, in which case the caller reports the bare
// exit status exactly as before.
func workerLastWords(configDir, dir, sessionID, stderrTail string) string {
	if msg := lastAssistantText(configDir, dir, sessionID); msg != "" {
		return msg
	}
	return truncateForNote(stderrTail)
}

// lastAssistantText returns the text of the LAST assistant message in a worker's
// transcript. It reuses transcriptCandidates, so it looks in the same places
// (project config dir, then the default; cwd both as given and symlink-resolved)
// as the structured-result recovery.
func lastAssistantText(configDir, dir, sessionID string) string {
	if sessionID == "" || dir == "" {
		return ""
	}
	for _, path := range transcriptCandidates(configDir, dir, sessionID) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		msg := scanLastAssistantText(f)
		f.Close()
		if msg != "" {
			return msg
		}
	}
	return ""
}

// scanLastAssistantText walks a transcript and returns the last assistant
// message's text. Tool-use blocks are ignored - the question being answered is
// "what did it SAY", and a tool call says nothing a human can read. Unparsable
// lines are skipped: a transcript is append-only and a killed worker can leave
// one truncated mid-write.
func scanLastAssistantText(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), transcriptScanCap)
	var last string
	for sc.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "assistant" {
			continue
		}
		var parts []string
		for _, c := range rec.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		if len(parts) > 0 {
			last = strings.Join(parts, "\n")
		}
	}
	return truncateForNote(last)
}

// truncateForNote bounds a message destined for a sub's note. The note is read
// on a board card and in a journal line, and the whole point is the first
// sentence.
func truncateForNote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= workerDeathTailBytes {
		return s
	}
	return strings.TrimSpace(s[:workerDeathTailBytes]) + " [...]"
}

// accountWalls maps a substring of a worker's last words to the reason pm
// reports. These are ACCOUNT-level walls: the next worker, started seconds
// later, hits exactly the same one, so continuing the run only converts the
// remaining subs into identical failures.
//
// Matching is on the message TEXT, and that is a deliberate compromise rather
// than a preference. The process signal carries nothing: a wall exits 1, like a
// crash, a bad flag and a worker that simply gave up. The text can change on
// Claude Code's side, so this list will go stale - which is why the failure mode
// is chosen carefully: an unrecognised wall behaves exactly as pm did before
// (one failed sub, the run continues), never worse. A false POSITIVE would be
// the damaging direction, so the phrases are specific enough not to appear in a
// worker's ordinary prose about its own task.
var accountWalls = []struct{ match, reason string }{
	{"hit your monthly spend limit", "the account's monthly spend limit is reached"},
	{"credit balance is too low", "the account's credit balance is exhausted"},
	{"usage limit reached", "the account's usage limit is reached"},
	{"please run /login", "the worker is not authenticated"},
	{"invalid api key", "the worker's credentials were rejected"},
	{"oauth token has expired", "the worker's credentials have expired"},
}

// accountWallReason returns a human reason when a worker's last words name an
// account-level wall, or "" when they do not.
func accountWallReason(lastWords string) string {
	low := strings.ToLower(lastWords)
	for _, w := range accountWalls {
		if strings.Contains(low, w.match) {
			return w.reason
		}
	}
	return ""
}

// accountWallError marks a worker death that WILL repeat for every remaining
// sub. The manager unwraps it (errors.As) and stops the run instead of feeding
// the wall one sub at a time: after the first wall in run pm-cli-74 the manager
// spawned three more workers, which died 112 s, 2 s and 7 s later.
type accountWallError struct {
	reason    string // what to tell the human
	lastWords string // the worker's own message, kept verbatim for the note
	err       error  // the underlying exit error
}

func (e *accountWallError) Error() string {
	return fmt.Sprintf("worker hit an account wall (%s): %s", e.reason, e.lastWords)
}

func (e *accountWallError) Unwrap() error { return e.err }
