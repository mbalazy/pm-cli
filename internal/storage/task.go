package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

// ValidateStatus checks if a status is in the allowed list.
func ValidateStatus(s TaskStatus, allowed []TaskStatus) error {
	for _, a := range allowed {
		if a == s {
			return nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return fmt.Errorf("invalid status %q (valid: %s)", s, strings.Join(names, ", "))
}

type TaskStatus string

const (
	StatusTodo     TaskStatus = "todo"
	StatusDoing    TaskStatus = "doing"
	StatusWaiting  TaskStatus = "waiting"
	StatusDone     TaskStatus = "done"
	StatusArchived TaskStatus = "archived"
	// StatusMerged is the per-project epic-lifecycle status (todo -> doing ->
	// merged -> done) used by pm run-epic's default done_status. It is NOT in
	// DefaultStatuses - it only exists on projects that opt into it via
	// project.yaml's `statuses` list.
	StatusMerged TaskStatus = "merged"
	// StatusPushed is the independent (batch) counterpart of StatusMerged: the
	// manager pushed the sub's own branch and merged nothing, so the work is on
	// origin awaiting a human's acceptance + PR. Same deal as StatusMerged - a
	// per-project status, opted into via project.yaml.
	//
	// Both constants exist for display only (glyphs, default configs). Code that
	// asks "did the executor finish this task" must resolve the project's
	// Executor.LandingStatuses(), never compare against these literals.
	StatusPushed TaskStatus = "pushed"
)

func ParseStatus(s string) TaskStatus {
	return TaskStatus(strings.ToLower(s))
}

type TaskMeta struct {
	ID       string            `yaml:"id"`
	Title    string            `yaml:"title"`
	Status   TaskStatus        `yaml:"status"`
	Created  string            `yaml:"created"`
	Updated  string            `yaml:"updated"`
	Links    map[string]string `yaml:"links,omitempty"`
	Branch   string            `yaml:"branch,omitempty"`
	Parent   string            `yaml:"parent,omitempty"`
	Tags     []string          `yaml:"tags,omitempty"`
	Brief    string            `yaml:"brief,omitempty"`
	AC       string            `yaml:"ac,omitempty"`
	Order    int               `yaml:"order,omitempty"`
	Sessions []string          `yaml:"sessions,omitempty"`
	// DependsOn lists sub IDs that must be merged/done before this sub runs.
	// Used by `pm run-epic`: an unsatisfied dependency parks the sub (skipped)
	// instead of spawning a doomed worker. Empty = no gate (runs by Order).
	DependsOn []string `yaml:"depends_on,omitempty"`
	// Mode marks a sub as autonomous ("auto"/empty) or human-only ("manual").
	// A "manual" sub is a permanent gate for `pm run-epic`: the manager skips it
	// entirely (no worker spawned, status untouched) on every run until a human
	// does the work and moves it to the done status themselves. Empty = "auto"
	// (backward compatible - existing subs run exactly as before).
	Mode string `yaml:"mode,omitempty"`
	// Model overrides the worker model for THIS sub when `pm run-epic` (or
	// `pm work` without an explicit --model) spawns it. Any claude model alias
	// or full name (e.g. "sonnet", "opus", "haiku"). Empty = inherit the
	// run-level model. Lets a batch put trivial subs (copy/color tweaks) on a
	// cheaper model while investigation subs stay on the default.
	Model string `yaml:"model,omitempty"`
	// EpicMode (meaningful on a PARENT tracker only) selects how `pm run-epic`
	// drives the subs. "" (default) = integration mode: subs branch off and merge
	// back into a shared epic/<tracker> branch, ending in one epic PR.
	// "independent" = batch mode for UNRELATED tasks: each sub gets its own
	// fresh branch off the base branch, is pushed to origin when it carries
	// commits, and nothing is merged - no integration branch, no epic PR; a
	// human finishes each task on its own branch later.
	EpicMode string `yaml:"epic_mode,omitempty"`
}

// EpicModeIndependent is the epic_mode value that switches `pm run-epic` to
// independent (batch) mode. Empty = the default integration mode.
const EpicModeIndependent = "independent"

// ValidateEpicMode checks a tracker's epic_mode field. Empty means integration
// mode (default, backward compatible).
func ValidateEpicMode(m string) error {
	switch m {
	case "", EpicModeIndependent:
		return nil
	}
	return fmt.Errorf("invalid epic_mode %q (valid: independent, or empty for integration mode)", m)
}

// ValidateMode checks a task's mode field. Empty means "auto" (default,
// backward compatible). Only "auto" and "manual" are valid (lowercase).
func ValidateMode(m string) error {
	switch m {
	case "", "auto", "manual":
		return nil
	}
	return fmt.Errorf("invalid mode %q (valid: auto, manual)", m)
}

// slugPattern is deliberately NOT "slug == Slugify(slug)": Slugify drops
// literal hyphens (it only turns ' '/'_'/'/' into '-', see the "slashes" case
// in TestSlugify: "feat/add-auth" -> "feat-addauth"), so that check would
// reject real hyphenated slugs like "app-orbit". Requiring the first
// char to be alphanumeric (never '.' or '/') is what blocks every path
// separator and ".."/"../foo" style traversal in one rule.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateSlug checks that a project slug is safe to use as a filesystem
// path component (ProjectDir/ProjectYAML join it under the pm root).
func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("invalid slug %q - must start with a lowercase letter or digit and contain only lowercase letters, digits, '.', '_' or '-' (no path separators)", slug)
	}
	return nil
}

// taskIDPattern is slugPattern's shape, deliberately WIDER: a task ID is a
// path component too (Filename renders "<id>-<title-slug>.md" under the
// project dir) but, unlike a slug, is not always minted by pm - real data
// holds legacy numeric IDs ("30422") and imported ticket keys ("ACME-253"), so
// digits-only and uppercase must both stay legal. Requiring the first
// character to be alphanumeric is what rejects "." / ".." / "../foo" and any
// absolute path in one rule; excluding '/' from the body blocks the rest.
var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateTaskID checks that a task ID is safe as the leading component of the
// task's file name. AddTask enforces it on every path an ID reaches it by: the
// `--id` flag, MCP's `id` param and the board's add prompt (all three take it
// from OUTSIDE pm) plus NextTaskID/NextChildID, whose "<prefix>-<n>" inherits
// the project's unvalidated `prefix` field. `pm add demo x --id ../../escaped`
// used to report success and write the task file two directories above the pm
// root, where no ReadTasksFromDir would ever find it again.
//
// Spaces and non-ASCII are rejected DELIBERATELY, not incidentally: such IDs
// were technically functional (Filename only concatenates, and the ID is read
// back from frontmatter, not from the name), but they are quoted into shell
// commands, branch names and log paths all over the executor, and ValidateSlug
// - the precedent this mirrors - draws the line in the same place. Widening it
// later is safe; narrowing it after IDs exist is not.
func ValidateTaskID(id string) error {
	if !taskIDPattern.MatchString(id) {
		return fmt.Errorf("invalid task id %q - must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (no path separators)", id)
	}
	return nil
}

// ValidateProjectPrefix checks a project's `prefix` field, which is the ID
// SOURCE for every auto-minted task ("<prefix>-<n>" from NextTaskID). It has
// to pass the same bar as an ID: an unsafe prefix used to produce merely ugly
// task files, but now that AddTask validates, it would lock the project out of
// task creation entirely - and every add would fail naming an ID nobody typed.
// Empty is fine (the slug is used, and slugs are validated on create).
func ValidateProjectPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if err := ValidateTaskID(prefix); err != nil {
		return fmt.Errorf("invalid prefix %q - task ids are minted as \"<prefix>-<n>\", so the prefix must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (no path separators or spaces)", prefix)
	}
	return nil
}

type Task struct {
	Meta     TaskMeta
	Body     string
	FilePath string
	Project  string
}

func (t *Task) Filename() string {
	slug := Slugify(t.Meta.Title)
	if t.Meta.ID != "" {
		return t.Meta.ID + "-" + slug + ".md"
	}
	return slug + ".md"
}

// Slugify converts a string to a URL/branch-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r == ' ' || r == '_' || r == '/' {
			return '-'
		}
		return -1
	}, s)
	// collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
		// Back up to the last word boundary so we don't cut mid-word
		// (e.g. "...7day-t" -> "...7day"). Keep the hard cut if the first
		// 60 chars are a single dashless word.
		if i := strings.LastIndex(s, "-"); i > 0 {
			s = s[:i]
		}
		s = strings.Trim(s, "-")
	}
	return s
}

func ReadTask(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// A .md with no frontmatter block is NOT a task (a stray README, a note
	// dropped into the data dir): frontmatter.Parse would happily return the
	// whole file as body with a zero meta, creating a phantom task with an
	// empty ID that half the code paths cannot address.
	if !strings.HasPrefix(strings.TrimLeft(string(data), "\n"), "---") {
		return nil, fmt.Errorf("parse %s: no frontmatter block", path)
	}

	var meta TaskMeta
	body, err := frontmatter.Parse(strings.NewReader(string(data)), &meta)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &Task{
		Meta:     meta,
		Body:     strings.TrimSpace(string(body)),
		FilePath: path,
	}, nil
}

// WriteTask is the exported package-level writer, used by tests and setupTestStore.
// Production code should use Store.WriteTask() or Store.AddTask() instead.
func WriteTask(t *Task) error {
	return writeTask(t)
}

func writeTask(t *Task) error {
	if err := ValidateMode(t.Meta.Mode); err != nil {
		return err
	}
	if err := ValidateEpicMode(t.Meta.EpicMode); err != nil {
		return err
	}
	metaBytes, err := yaml.Marshal(t.Meta)
	if err != nil {
		return err
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")
	if t.Body != "" {
		buf.WriteString("\n")
		buf.WriteString(t.Body)
		buf.WriteString("\n")
	}

	return atomicWriteFile(t.FilePath, []byte(buf.String()), 0644)
}

// atomicWriteFile writes data to a temp file then renames it to path.
// This prevents partial writes on crash.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pm-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Today returns the current date as YYYY-MM-DD.
func Today() string {
	return time.Now().Format("2006-01-02")
}

func NewTask(id, title, project string) *Task {
	now := Today()
	return &Task{
		Meta: TaskMeta{
			ID:      id,
			Title:   title,
			Status:  StatusTodo,
			Created: now,
			Updated: now,
		},
		Project: project,
	}
}

func ReadTasksFromDir(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "project.yaml" {
			continue
		}
		t, err := ReadTask(filepath.Join(dir, e.Name()))
		if err != nil {
			// Skip, but never SILENTLY: a hand-edit that broke the YAML used to
			// make the task vanish from every list/board/rollup with no trace -
			// indistinguishable from deletion until someone opened the file.
			fmt.Fprintf(os.Stderr, "pm: skipping unreadable task file: %v\n", err)
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
