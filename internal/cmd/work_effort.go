package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// recoverWorkerEffort reads a dead worker's own transcript and returns how much
// work it did, for the one case pm otherwise reports as nothing at all: a worker
// that died without a result envelope, whose turns and cost are therefore 0 (see
// storage.WorkerEffort for the measurement and its limits).
//
// It works for the same reason workerLastWords does: pm MINTS the session id and
// pins it with `claude --session-id` before the worker starts, so the transcript
// path is known even when nothing came back on stdout. The lookup reuses
// transcriptCandidates, so a new place a transcript can live is added once.
//
// Returns nil when there is nothing to report - no session, no transcript, or a
// transcript with no assistant record in it. Nil is the honest answer there: a
// zero-filled struct would claim pm measured a worker that did nothing, which is
// the very ambiguity this exists to remove.
func recoverWorkerEffort(configDir, dir, sessionID string) *storage.WorkerEffort {
	var found *storage.WorkerEffort
	scanTranscripts(configDir, dir, sessionID, func(r io.Reader) bool {
		found = scanWorkerEffort(r)
		return found != nil
	})
	return found
}

// effortSourceTranscript names the only source pm reconstructs effort from.
const effortSourceTranscript = "transcript"

// scanWorkerEffort sums the main loop's input turns and output tokens over a
// transcript stream.
//
// Sidechain records are skipped: a subagent's turns are not the worker's own
// (the envelope's num_turns does not count them either), which is why the token
// figure is documented as a floor rather than a total.
//
// Unparsable lines are skipped - a transcript is append-only and a worker killed
// mid-write leaves the last line truncated, which is precisely the population
// this function is for.
func scanWorkerEffort(r io.Reader) *storage.WorkerEffort {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), transcriptScanCap)
	turns, tokens, assistants := 0, 0, 0
	for sc.Scan() {
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.IsSidechain {
			continue
		}
		switch rec.Type {
		case "user":
			turns++
		case "assistant":
			assistants++
			tokens += rec.Message.Usage.OutputTokens
		}
	}
	if assistants == 0 {
		return nil
	}
	return &storage.WorkerEffort{Source: effortSourceTranscript, Turns: turns, OutputTokens: tokens}
}

// setSubEffort records reconstructed effort on sub id in a run-state. Called
// under the run writer's lock, like setSubStats - and by id rather than by index
// because the same call has to serve a standalone run (one sub) and an epic
// manager's shared run-state (one entry per sub).
func setSubEffort(run *storage.RunState, id string, effort *storage.WorkerEffort) {
	for i := range run.Subs {
		if run.Subs[i].ID == id {
			run.Subs[i].Effort = effort
		}
	}
}

// describeEffort renders reconstructed effort for a human-facing line, or "" for
// nil. Kept next to the measurement so the number and its caveat cannot drift
// apart: whoever reads it has to know the cost is missing, not zero.
func describeEffort(e *storage.WorkerEffort) string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("effort reconstructed from the transcript: %d turn(s), %d output token(s) - the worker sent no envelope, so its cost is unknown, not zero",
		e.Turns, e.OutputTokens)
}
