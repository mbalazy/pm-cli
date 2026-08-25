package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// claudeOutput is what `claude -p --output-format json` left on stdout, reduced
// to the two things every headless run reads off it: the result envelope (the
// `type: "result"` message, whose `structured_output` carries the run's typed
// contract) and the session id.
//
// The CLI has written this in two shapes. Up to some 2.1.x release the whole
// document WAS the result envelope - one JSON object. Claude Code 2.1.237
// (measured 2026-08-20, pm-cli-117) writes a JSON ARRAY of every message of the
// run - `system` init/retry records, the assistant turns - and the familiar
// envelope is its LAST element. pm decoded the bytes straight into the envelope
// struct, so the array failed the parse, the session id went with it, and a run
// whose two workers had both finished (one of them pushed) recorded both as
// `failed` with "cannot unmarshal array into Go value of type cmd.claudeEnvelope"
// - and wrote nothing into either task.
//
// Both shapes are accepted, by inspecting the first byte rather than trying one
// after the other: the object form is what every older CLI emits and what every
// fake in the test suite speaks, and the array form is what the CLI emits now.
// SessionID is filled from WHATEVER the bytes hold - the envelope's own field,
// any message of the array that carries one, or as a last resort a textual scan
// for `"session_id": "..."` - even when the document does not decode at all.
// That is the half that matters more than the array support: the transcript
// recovery in parseWorkerOutput/parseFinishOutput is keyed on the session id,
// so an id that survives the parse failure lets pm recover the worker's real
// result from its transcript under the NEXT unexpected shape too, instead of
// only this one.
type claudeOutput struct {
	// Result is the `type: "result"` envelope, or nil when none was found.
	Result json.RawMessage
	// SessionID is the best session id the bytes yielded; "" when none.
	SessionID string
}

// claudeMessageHead is the lenient per-message view the decoder uses to find
// the result among an array's messages and a session id anywhere in it.
type claudeMessageHead struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

// sessionIDPattern is the last-resort session id scan over bytes that did not
// decode: a `session_id` key whose value looks like the UUID the CLI mints.
// Anchored on the key, so prose mentioning a session elsewhere does not match.
var sessionIDPattern = regexp.MustCompile(`"session_id"\s*:\s*"([0-9a-fA-F-]{8,})"`)

// decodeClaudeOutput reduces the CLI's stdout to a claudeOutput. The error says
// why no result envelope could be located; SessionID is populated on a best-
// effort basis REGARDLESS of the error, which callers rely on.
func decodeClaudeOutput(data []byte) (claudeOutput, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return claudeOutput{}, fmt.Errorf("claude wrote no output")
	}
	var out claudeOutput
	var err error
	switch trimmed[0] {
	case '[':
		var msgs []json.RawMessage
		if uerr := json.Unmarshal(trimmed, &msgs); uerr != nil {
			err = fmt.Errorf("claude output is not a well-formed message array: %w", uerr)
			break
		}
		for _, m := range msgs {
			var head claudeMessageHead
			if json.Unmarshal(m, &head) != nil {
				continue
			}
			if head.SessionID != "" && out.SessionID == "" {
				out.SessionID = head.SessionID
			}
			// LAST result wins: the CLI writes exactly one, at the end, but if a
			// future shape ever carried several, the final one is the run's.
			if head.Type == "result" {
				out.Result = m
				if head.SessionID != "" {
					out.SessionID = head.SessionID
				}
			}
		}
		if out.Result == nil {
			err = fmt.Errorf("claude output is a %d-message array with no result message", len(msgs))
		}
	case '{':
		var head claudeMessageHead
		if uerr := json.Unmarshal(trimmed, &head); uerr != nil {
			err = fmt.Errorf("claude output is not a well-formed result object: %w", uerr)
			break
		}
		out.Result = trimmed
		out.SessionID = head.SessionID
	default:
		err = fmt.Errorf("claude output is neither a JSON object nor an array")
	}
	if out.SessionID == "" {
		if m := sessionIDPattern.FindSubmatch(data); m != nil {
			out.SessionID = string(m[1])
		}
	}
	return out, err
}

// envelopeStats lifts the harness-owned run stats (turns, cost) off whatever
// result envelope the bytes hold, for a result recovered from the transcript
// rather than from the envelope. Zero values when there is no envelope: the
// recovered result is then a transcript's word, and its cost is unknown.
func envelopeStats(data []byte) (turns int, costUSD float64) {
	out, err := decodeClaudeOutput(data)
	if err != nil {
		return 0, 0
	}
	var env struct {
		NumTurns     int     `json:"num_turns"`
		TotalCostUSD float64 `json:"total_cost_usd"`
	}
	if json.Unmarshal(out.Result, &env) != nil {
		return 0, 0
	}
	return env.NumTurns, env.TotalCostUSD
}
