package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newSessionIDCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-id",
		Short: "Detect current Claude Code session UUID",
		Long:  "Prints the session UUID of the Claude Code instance running this command. Walks the process tree to find a claude --resume arg, or falls back to the most recently modified session file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("CLAUDECODE") != "1" {
				return fmt.Errorf("not running inside Claude Code")
			}

			sessionID, err := detectSession()
			if err != nil {
				return err
			}

			fmt.Print(sessionID)
			return nil
		},
	}
}

func detectSession() (string, error) {
	// Strategy 1: Walk process tree looking for "claude --resume <id>"
	if sid, ok := sessionFromProcessTree(); ok {
		return sid, nil
	}

	// Strategy 2: Most recently modified .jsonl in CC project dir
	if sid, ok := sessionFromProjectDir(); ok {
		return sid, nil
	}

	return "", fmt.Errorf("could not detect session ID")
}

// sessionFromProcessTree walks ancestors looking for "claude --resume <session-id>".
func sessionFromProcessTree() (string, bool) {
	pid := os.Getppid()
	for range 5 {
		if pid <= 1 {
			break
		}
		out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			break
		}
		args := strings.TrimSpace(string(out))
		if isClaudeProcess(args) {
			if sid := parseResumeArg(args); sid != "" {
				return sid, true
			}
		}
		parent, err := parentPID(pid)
		if err != nil || parent == pid {
			break
		}
		pid = parent
	}
	return "", false
}

func isClaudeProcess(cmdline string) bool {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	return base == "claude"
}

// parseResumeArg extracts session ID from "claude --resume <id>" or "claude -c <id>".
func parseResumeArg(cmdline string) string {
	fields := strings.Fields(cmdline)
	for i, f := range fields {
		if (f == "--resume" || f == "-r" || f == "--continue" || f == "-c") && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// sessionFromProjectDir finds the most recently modified .jsonl in the CC project sessions dir.
// On mtime tie, prefers the larger file (main session > background tasks).
func sessionFromProjectDir() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	projectKey := encodeProjectPath(cwd)
	sessDir := filepath.Join(home, ".claude", "projects", projectKey)

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return "", false
	}

	var bestName string
	var bestMod int64
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().Unix()
		size := info.Size()
		if mod > bestMod || (mod == bestMod && size > bestSize) {
			bestMod = mod
			bestSize = size
			bestName = e.Name()
		}
	}

	if bestName == "" {
		return "", false
	}
	return strings.TrimSuffix(bestName, ".jsonl"), true
}

// encodeProjectPath converts a filesystem path to CC's project directory name.
// /Users/alice/.claude/pm-cli -> -Users-mart--claude-pm-cli
func encodeProjectPath(path string) string {
	// Strip leading /
	p := strings.TrimPrefix(path, "/")
	// Replace "/." with "--" (hidden dirs like .claude, .config)
	p = strings.ReplaceAll(p, "/.", "--")
	// Replace remaining "/" with "-"
	p = strings.ReplaceAll(p, "/", "-")
	return "-" + p
}

func parentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
