// Package ccpath maps a filesystem path to the directory name Claude Code
// keeps that path's session transcripts under (~/.claude/projects/<name>).
package ccpath

import "strings"

// Encode converts a filesystem path to CC's project directory name:
// every "/" and "." becomes "-". Matches board.pathToCCProject.
//
//	/Users/alice/.claude/pm-cli      -> -Users-alice--claude-pm-cli
//	/Users/alice/repos/x/app.mobile  -> -Users-alice-repos-x-app-mobile
func Encode(path string) string {
	s := strings.ReplaceAll(path, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}
