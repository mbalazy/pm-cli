package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// parseWorkerOutput turns a `claude -p --output-format json` envelope into a
// worker result, recovering the result from the worker's transcript when the
// envelope does not carry one.
//
// Why the recovery exists: the envelope's structured_output holds the schema
// result only while the StructuredOutput call is the worker's LAST act. Anything
// that forces one more turn after it - most easily a background Bash task whose
// completion notification lands late - leaves structured_output null and the
// plain final sentence in result. pm then reported a fully finished sub as
// "worker returned no structured result" and moved it to failed: the work was
// committed and pushed, only the receipt was lost. Observed 2026-08-05 on
// orbit-vps-1-1, whose recorded status contradicted its own transcript (the worker
// had emitted status "verified" with branch and commit twelve seconds earlier).
//
// The transcript is the same data the envelope would have carried, written by
// the harness rather than restated by the model, so honouring it is not a guess.
// A recovery is always announced on errOut - a silent one would hide the real
// defect (the worker's turn order) behind pm papering over it.
func parseWorkerOutput(data []byte, errOut io.Writer, configDir, dir string) (*workerResult, string, error) {
	res, sessionID, err := parseClaudeResult(data)
	if err == nil || sessionID == "" {
		return res, sessionID, err
	}
	rec, path := lastStructuredOutput(configDir, dir, sessionID)
	if rec == nil {
		return res, sessionID, err
	}
	var env claudeEnvelope
	if jsonErr := json.Unmarshal(data, &env); jsonErr == nil {
		rec.Turns = env.NumTurns
		rec.CostUSD = env.TotalCostUSD
	}
	rec.Status = normalizeWorkerStatus(rec.Status)
	if errOut != nil {
		fmt.Fprintf(errOut, "pm work: envelope carried no structured result - recovered status %q from the worker's last StructuredOutput call in %s (the worker kept talking after emitting it, most likely a late background-task notification)\n", rec.Status, path)
	}
	return rec, sessionID, nil
}

// transcriptScanCap bounds a single transcript line. Worker transcripts embed
// whole file reads and command output, so lines run far past bufio's 64KB
// default; a line that still exceeds this is skipped, not fatal.
const transcriptScanCap = 8 << 20

// lastStructuredOutput scans a worker's Claude Code transcript for the LAST
// StructuredOutput tool call and decodes its input as a worker result. Returns
// the result and the transcript path it came from, or nil when there is no
// transcript or no usable call in it.
//
// LAST, not first: a worker that revises its verdict (a fix after a red gate)
// calls the tool more than once, and the final call is the one the harness would
// have reported.
func lastStructuredOutput(configDir, dir, sessionID string) (*workerResult, string) {
	if sessionID == "" || dir == "" {
		return nil, ""
	}
	for _, path := range transcriptCandidates(configDir, dir, sessionID) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		res := scanStructuredOutput(f)
		f.Close()
		if res != nil {
			return res, path
		}
	}
	return nil, ""
}

// transcriptCandidates lists the paths a worker transcript can live at: the
// project's Claude config dir first (a project pinned to e.g. ~/.claude-alt
// writes there), then the default. The worker's cwd is the encoded directory
// name, resolved both as given and with symlinks expanded - Claude Code encodes
// the path it actually runs in, which on macOS differs for /tmp and friends.
func transcriptCandidates(configDir, dir, sessionID string) []string {
	dirs := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		dirs = append(dirs, real)
	}
	cfgs := []string{}
	def := storage.DefaultClaudeConfigDir()
	if configDir != "" {
		cfgs = append(cfgs, configDir)
	}
	if configDir != def {
		cfgs = append(cfgs, def)
	}
	var out []string
	for _, cfg := range cfgs {
		for _, d := range dirs {
			out = append(out, filepath.Join(cfg, "projects", encodeProjectPath(d), sessionID+".jsonl"))
		}
	}
	return out
}

// scanStructuredOutput returns the last decodable StructuredOutput tool input in
// a transcript stream, or nil. A record that fails to parse is skipped: a
// transcript is append-only and can be truncated mid-write by a killed worker.
func scanStructuredOutput(r io.Reader) *workerResult {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), transcriptScanCap)
	var found *workerResult
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), "StructuredOutput") {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type  string          `json:"type"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
			continue
		}
		for _, c := range rec.Message.Content {
			if c.Type != "tool_use" || c.Name != "StructuredOutput" || len(c.Input) == 0 {
				continue
			}
			var res workerResult
			if json.Unmarshal(c.Input, &res) != nil {
				continue
			}
			// A call with no status carries no verdict - the schema requires one,
			// so an empty one means this is not the result the harness wanted.
			if strings.TrimSpace(res.Status) == "" {
				continue
			}
			found = &res
		}
	}
	return found
}
