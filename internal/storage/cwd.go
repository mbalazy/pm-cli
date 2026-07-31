package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveProjectFromCwd finds the project whose Path most specifically
// encloses cwd: both sides are tilde-expanded and made absolute, a match
// requires cwd to be the project path itself or a true subdirectory of it
// (filepath.Rel boundary check, so a sibling that merely shares a name
// prefix - e.g. "pm-cli-old" vs "pm-cli" - never matches), and when more
// than one project encloses cwd the one with the longest (deepest) path
// wins.
func ResolveProjectFromCwd(store TaskStore, cwd string) (string, error) {
	cwd, err := filepath.Abs(expandTilde(cwd))
	if err != nil {
		return "", err
	}
	projects, err := store.ListProjects()
	if err != nil {
		return "", err
	}

	bestSlug := ""
	bestLen := -1
	for _, slug := range projects {
		proj, err := store.GetProject(slug)
		if err != nil || proj == nil || proj.Path == "" {
			continue
		}
		projectPath, err := filepath.Abs(expandTilde(proj.Path))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(projectPath, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(projectPath) > bestLen {
			bestLen = len(projectPath)
			bestSlug = slug
		}
	}
	if bestSlug == "" {
		return "", fmt.Errorf("no project found for: %s", cwd)
	}
	return bestSlug, nil
}

// expandTilde resolves a leading "~" or "~/" to the current user's home directory.
func expandTilde(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
