package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// arrayEnvelope renders the message-array shape Claude Code 2.1.237 writes for
// `-p --output-format json` (pm-cli-117): system records first, then the
// assistant turn, then the familiar result envelope as the LAST element.
// resultTail is spliced into that last element after session_id.
func arrayEnvelope(resultTail string) string {
	return `[` +
		`{"type":"system","subtype":"init","session_id":"sess-arr","cwd":"/x","tools":[]},` +
		`{"type":"system","subtype":"api_retry","session_id":"sess-arr","attempt":1},` +
		`{"type":"assistant","session_id":"sess-arr","message":{"content":[{"type":"text","text":"ok"}]}},` +
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-arr","num_turns":7,"total_cost_usd":1.5` + resultTail + `}` +
		`]`
}

func TestDecodeClaudeOutput(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantSID   string
		wantRes   bool
		wantErr   string
		resultHas string
	}{
		{
			name:    "object shape (older CLIs, every fake in this suite)",
			in:      `{"type":"result","session_id":"sess-obj","num_turns":2}`,
			wantSID: "sess-obj", wantRes: true, resultHas: `"num_turns":2`,
		},
		{
			name:    "array shape (CLI 2.1.237): result is the last element",
			in:      arrayEnvelope(""),
			wantSID: "sess-arr", wantRes: true, resultHas: `"total_cost_usd":1.5`,
		},
		{
			name:    "array without a result message still yields the session id",
			in:      `[{"type":"system","subtype":"init","session_id":"sess-cut"},{"type":"assistant","session_id":"sess-cut"}]`,
			wantSID: "sess-cut", wantErr: "no result message",
		},
		{
			name:    "truncated array: session id scanned out of the bytes",
			in:      `[{"type":"system","subtype":"init","session_id":"0b8c1d2e-1111-2222-3333-444455556666"},{"type":"assist`,
			wantSID: "0b8c1d2e-1111-2222-3333-444455556666", wantErr: "not a well-formed message array",
		},
		{
			name:    "garbage with no id anywhere",
			in:      "not json at all",
			wantErr: "neither a JSON object nor an array",
		},
		{
			name:    "empty output",
			in:      "   \n",
			wantErr: "no output",
		},
		{
			name:    "object with no session id at all",
			in:      `{"type":"result","result":"x"}`,
			wantRes: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := decodeClaudeOutput([]byte(tc.in))
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
			if out.SessionID != tc.wantSID {
				t.Errorf("session id = %q, want %q", out.SessionID, tc.wantSID)
			}
			if (out.Result != nil) != tc.wantRes {
				t.Errorf("result present = %v, want %v", out.Result != nil, tc.wantRes)
			}
			if tc.resultHas != "" && !bytes.Contains(out.Result, []byte(tc.resultHas)) {
				t.Errorf("result %s does not contain %q", out.Result, tc.resultHas)
			}
		})
	}
}

func TestEnvelopeStatsReadBothShapes(t *testing.T) {
	if turns, cost := envelopeStats([]byte(arrayEnvelope(""))); turns != 7 || cost != 1.5 {
		t.Errorf("array shape: turns=%d cost=%v, want 7/1.5", turns, cost)
	}
	if turns, cost := envelopeStats([]byte(envelopeNoResult)); turns != 41 || cost != 2.75 {
		t.Errorf("object shape: turns=%d cost=%v, want 41/2.75", turns, cost)
	}
	if turns, cost := envelopeStats([]byte("garbage")); turns != 0 || cost != 0 {
		t.Errorf("no envelope: turns=%d cost=%v, want 0/0", turns, cost)
	}
}

func TestParseClaudeResultArrayShape(t *testing.T) {
	t.Run("structured output inside the last element", func(t *testing.T) {
		data := arrayEnvelope(`,"structured_output":{"status":"merged","summary":"done","branch":"feat/a","commits":["c1"],"unresolved":[]}`)
		res, sid, err := parseClaudeResult([]byte(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sid != "sess-arr" {
			t.Errorf("session id = %q, want sess-arr", sid)
		}
		if res.Status != workerVerified || res.Branch != "feat/a" || res.Turns != 7 || res.CostUSD != 1.5 {
			t.Errorf("result = %+v (status must be normalized, stats lifted)", res)
		}
	})
	t.Run("array with no structured output errors but keeps the session id", func(t *testing.T) {
		_, sid, err := parseClaudeResult([]byte(arrayEnvelope("")))
		if err == nil || !strings.Contains(err.Error(), "no structured result") {
			t.Fatalf("err = %v", err)
		}
		if sid != "sess-arr" {
			t.Errorf("session id = %q, want sess-arr", sid)
		}
	})
	t.Run("array with no result message keeps the session id", func(t *testing.T) {
		_, sid, err := parseClaudeResult([]byte(`[{"type":"system","subtype":"init","session_id":"sess-cut"}]`))
		if err == nil {
			t.Fatal("expected an error")
		}
		if sid != "sess-cut" {
			t.Errorf("session id = %q, want sess-cut - it is what the transcript recovery needs", sid)
		}
	})
}

// The pm-cli-117 run, end to end at the parser: an array envelope pm could not
// decode before, a worker that had finished (its transcript holds the
// StructuredOutput call), and a result pm now recovers instead of "failed".
func TestParseWorkerOutputArrayEnvelopeRecoversFromTranscript(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path := writeTranscript(t, cfg, dir, "sess-arr", transcriptLine(t, map[string]any{
		"status": "verified", "summary": "pushed", "branch": "me-login/acme-1671", "commits": []string{"2b499fea"}, "unresolved": []string{},
	}))

	var log bytes.Buffer
	res, sid, err := parseWorkerOutput([]byte(arrayEnvelope("")), &log, cfg, dir, "")
	if err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	if sid != "sess-arr" || res.Status != workerVerified || res.Branch != "me-login/acme-1671" {
		t.Errorf("sid=%q res=%+v", sid, res)
	}
	if res.Turns != 7 || res.CostUSD != 1.5 {
		t.Errorf("stats must be lifted from the array's result element: turns=%d cost=%v", res.Turns, res.CostUSD)
	}
	if !strings.Contains(log.String(), path) {
		t.Errorf("recovery must name its source: %q", log.String())
	}
}

// The half that outlives this CLI version: when the bytes yield no session id
// at all, the id pm minted and pinned with --session-id still reaches the
// transcript recovery, and the announcement says the envelope did not decode.
func TestParseWorkerOutputUndecodableEnvelopeUsesPinnedSession(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeTranscript(t, cfg, dir, "sess-pinned", transcriptLine(t, map[string]any{
		"status": "blocked", "summary": "needs a human", "branch": "b", "commits": []string{}, "unresolved": []string{"TODO: x"},
	}))

	var log bytes.Buffer
	res, sid, err := parseWorkerOutput([]byte("<<some future shape>>"), &log, cfg, dir, "sess-pinned")
	if err != nil {
		t.Fatalf("expected recovery via the pinned id, got %v", err)
	}
	if sid != "sess-pinned" || res.Status != workerBlocked {
		t.Errorf("sid=%q res=%+v", sid, res)
	}
	if res.Turns != 0 || res.CostUSD != 0 {
		t.Errorf("no envelope = no stats, never invented ones: turns=%d cost=%v", res.Turns, res.CostUSD)
	}
	if !strings.Contains(log.String(), "could not decode the claude envelope") {
		t.Errorf("announcement must say the envelope did not decode: %q", log.String())
	}

	// Without a transcript the pinned id changes nothing: the original error
	// stands, and the id is still returned for the caller's record.
	_, sid, err = parseWorkerOutput([]byte("<<some future shape>>"), nil, cfg, t.TempDir(), "sess-other")
	if err == nil || !strings.Contains(err.Error(), "parse claude result") {
		t.Fatalf("err = %v", err)
	}
	if sid != "sess-other" {
		t.Errorf("session id = %q, want the pinned one", sid)
	}
}

func TestParseWorkerOutputEnvelopeIDBeatsPinned(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	_, sid, _ := parseWorkerOutput([]byte(envelopeNoResult), nil, cfg, dir, "sess-pinned")
	if sid != "sess-1" {
		t.Errorf("session id = %q, want the envelope's own (sess-1) over the pinned hint", sid)
	}
}

func TestParseFinishResultArrayShape(t *testing.T) {
	data := arrayEnvelope(`,"structured_output":{"status":"done","summary":"all accepted","report":"# R","subs":[]}`)
	res, sid, err := parseFinishResult([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "sess-arr" || res.Status != finishDone || res.Turns != 7 || res.CostUSD != 1.5 {
		t.Errorf("sid=%q res=%+v", sid, res)
	}
	_, sid, err = parseFinishResult([]byte(arrayEnvelope("")))
	if err == nil || sid != "sess-arr" {
		t.Errorf("no structured output: err=%v sid=%q, want an error and the session id", err, sid)
	}
}
