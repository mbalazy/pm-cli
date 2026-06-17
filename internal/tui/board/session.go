package board

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func lastSession(t *storage.Task) string {
	if len(t.Meta.Sessions) > 0 {
		return t.Meta.Sessions[len(t.Meta.Sessions)-1]
	}
	return t.Meta.Links["cc-session"] // legacy fallback
}

func (m *Model) saveSession(t *storage.Task, sessionID string) {
	// Migrate legacy cc-session link
	if old := t.Meta.Links["cc-session"]; old != "" {
		if len(t.Meta.Sessions) == 0 || t.Meta.Sessions[len(t.Meta.Sessions)-1] != old {
			t.Meta.Sessions = append(t.Meta.Sessions, old)
		}
		delete(t.Meta.Links, "cc-session")
		if len(t.Meta.Links) == 0 {
			t.Meta.Links = nil
		}
	}
	t.Meta.Sessions = append(t.Meta.Sessions, sessionID)
	t.Meta.Updated = storage.Today()
	m.store.WriteTask(t)
}

type sessionIndexEntry struct {
	SessionID    string `json:"sessionId"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
}

type sessionsIndex struct {
	Entries []sessionIndexEntry `json:"entries"`
}

// ccProjectDirs returns CC project directories to search for session data.
// Includes main project dir and all worktree dirs.
func ccProjectDirs(projDir string) []string {
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	var dirs []string
	dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(projDir)))
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	if entries, err := os.ReadDir(wtBase); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				wtPath := filepath.Join(wtBase, e.Name())
				dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(wtPath)))
			}
		}
	}
	return dirs
}

// resolveSessionPath returns the absolute path to the session JSONL file,
// or empty string if the file cannot be found.
func resolveSessionPath(projDir, sessionID string) string {
	if projDir == "" || sessionID == "" {
		return ""
	}
	for _, dir := range ccProjectDirs(projDir) {
		p := filepath.Join(dir, sessionID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// loadSessionMeta looks up session metadata from Claude Code's sessions-index.json.
// Falls back to parsing the JSONL file directly if not found in index.
func loadSessionMeta(projDir, sessionID string) *sessionIndexEntry {
	if projDir == "" || sessionID == "" {
		return nil
	}

	ccDirs := ccProjectDirs(projDir)

	// Try sessions-index.json first (fast path)
	for _, dir := range ccDirs {
		indexPath := filepath.Join(dir, "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			continue
		}
		var idx sessionsIndex
		if err := json.Unmarshal(data, &idx); err != nil {
			continue
		}
		for _, e := range idx.Entries {
			if e.SessionID == sessionID {
				return &e
			}
		}
	}

	// Fallback: parse JSONL file directly
	for _, dir := range ccDirs {
		jsonlPath := filepath.Join(dir, sessionID+".jsonl")
		info, err := os.Stat(jsonlPath)
		if err != nil {
			continue
		}
		entry := &sessionIndexEntry{
			SessionID: sessionID,
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
		}
		data, err := os.ReadFile(jsonlPath)
		if err != nil {
			return entry
		}
		msgCount := 0
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Type    string `json:"type"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if rec.Type == "user" || rec.Type == "assistant" {
				msgCount++
			}
			if rec.Type == "summary" && rec.Summary != "" {
				entry.Summary = rec.Summary
			}
		}
		entry.MessageCount = msgCount
		return entry
	}

	return nil
}

// findWorktreeForSession scans worktree subdirs under projDir and checks
// if Claude Code has a conversation file for the given session ID stored
// under a project directory corresponding to that worktree path.
// Returns the worktree absolute path if found, empty string otherwise.
func findWorktreeForSession(projDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	entries, err := os.ReadDir(wtBase)
	if err != nil {
		return ""
	}
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtPath := filepath.Join(wtBase, e.Name())
		// Claude Code maps absolute path to project dir name by replacing
		// "/" with "-" and stripping "." from directory names.
		ccDirName := pathToCCProject(wtPath)
		sessionFile := filepath.Join(ccProjectsDir, ccDirName, sessionID+".jsonl")
		if _, err := os.Stat(sessionFile); err == nil {
			return wtPath
		}
	}
	return ""
}

// findSessionDirGlobal scans all CC project directories (~/.claude/projects/*)
// for a session JSONL file. Returns the filesystem path that CC would need as
// cwd to find the session, or empty string if not found.
// Uses forward-matching: encodes candidate paths and compares against the CC dir
// name (decoding is ambiguous because pathToCCProject is lossy).
func findSessionDirGlobal(sessionID string, store storage.TaskStore) string {
	if sessionID == "" {
		return ""
	}
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	// Find which CC project dir has the session file
	var ccDirName string
	entries, err := os.ReadDir(ccProjectsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(ccProjectsDir, e.Name(), sessionID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			ccDirName = e.Name()
			break
		}
	}
	if ccDirName == "" {
		return ""
	}

	// Build candidate paths and check which one encodes to ccDirName.
	// 1. PM project paths
	if slugs, err := store.ListActiveProjects(); err == nil {
		for _, slug := range slugs {
			if proj, err := store.GetProject(slug); err == nil && proj.Path != "" {
				if abs, err := filepath.Abs(proj.Path); err == nil {
					if pathToCCProject(abs) == ccDirName {
						return abs
					}
				}
			}
		}
	}

	// 2. Common directories: ~, ~/*, ~/.claude/*
	for _, parent := range []string{homeDir, filepath.Join(homeDir, ".claude")} {
		if pathToCCProject(parent) == ccDirName {
			return parent
		}
		if children, err := os.ReadDir(parent); err == nil {
			for _, c := range children {
				if c.IsDir() {
					candidate := filepath.Join(parent, c.Name())
					if pathToCCProject(candidate) == ccDirName {
						return candidate
					}
				}
			}
		}
	}

	return ""
}

// pathToCCProject converts an absolute path to the Claude Code project
// directory name format: replace "/" and "." with "-".
func pathToCCProject(absPath string) string {
	s := strings.ReplaceAll(absPath, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}
