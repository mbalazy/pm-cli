// Package storage is pm's data layer, and the files are the only source of
// truth: a project is a directory under ~/.claude/pm/ (PM_DATA_DIR overrides
// the root), a task is a markdown file with YAML frontmatter sitting next to
// that project's project.yaml, and the journals, the timeline and the
// executor's run journal are append-only JSONL in that project's own
// subdirectories. A run's LIVE state is the exception: one JSON file
// rewritten whole (tmp + rename) on every 30-second heartbeat, so its
// earlier contents are gone, not recoverable from the file.
//
// Two rules shape almost everything here. Files are NEVER migrated between
// versions, so every reader tolerates an older shape and reports a missing
// field as unknown instead of guessing one. And a write that could race -
// a task edit, project.yaml - takes the project's flock through LockProject
// and re-reads the file fresh inside it, because a lost update here is
// somebody's brief. The acceptance claim does NOT use that flock: it is a
// create-if-absent os.Link in finish_claim.go, because a claim has to
// outlive the process that took it (another host must see it, and a stale
// one must be stealable), and a flock dies with its holder.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ErrAmbiguousTask is what FindTask returns (wrapped) when a query matches
// several tasks in ONE project. Callers that scan many projects test for it
// with errors.Is: an ambiguous project is a project with candidates, not a
// project with none.
var ErrAmbiguousTask = errors.New("ambiguous task")

// ErrTaskNotFound and ErrProjectNotFound are wrapped (message unchanged) into
// the not-found errors of FindTask/FindTaskExact and ResolveProject, so a
// transport can answer "404" without matching on prose. Sentinel TEXT is the
// leading phrase the messages always carried.
var (
	ErrTaskNotFound    = errors.New("task not found")
	ErrProjectNotFound = errors.New("project not found")
)

type Store struct {
	Root string // ~/.claude/pm
}

func NewStore() *Store {
	if dir := os.Getenv("PM_DATA_DIR"); dir != "" {
		return &Store{Root: dir}
	}
	home, _ := os.UserHomeDir()
	return &Store{Root: filepath.Join(home, ".claude", "pm")}
}

func (s *Store) RootDir() string {
	return s.Root
}

// warnLockDegrade reports a silent-degrade hazard: a caller that could not
// take the project lock and is about to write UNLOCKED anyway. Without this,
// a concurrent session's edit can get clobbered with no trace - see
// applyWorkerResult (internal/cmd/work.go), the one site that already got
// this right, whose wording this mirrors.
func warnLockDegrade(op, slug string, lockErr error) {
	fmt.Fprintf(os.Stderr, "pm: project lock unavailable for %q (%v) - %s without it\n", slug, lockErr, op)
}

func (s *Store) Init() error {
	return os.MkdirAll(s.Root, 0755)
}

func (s *Store) ProjectDir(slug string) string {
	return filepath.Join(s.Root, slug)
}

func (s *Store) ProjectYAML(slug string) string {
	return filepath.Join(s.Root, slug, "project.yaml")
}

// ListProjects returns all project directory slugs that contain a project.yaml.
func (s *Store) ListProjects() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var projects []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(s.ProjectYAML(e.Name())); err == nil {
			projects = append(projects, e.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

// ListActiveProjects returns project slugs excluding archived ones.
func (s *Store) ListActiveProjects() ([]string, error) {
	all, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	var active []string
	for _, slug := range all {
		proj, err := s.GetProject(slug)
		if err != nil || !proj.Archived {
			active = append(active, slug)
		}
	}
	return active, nil
}

func (s *Store) GetProject(slug string) (*Project, error) {
	return ReadProject(s.ProjectYAML(slug))
}

// GetProjectStatuses returns the configured statuses for a project, or defaults.
func (s *Store) GetProjectStatuses(slug string) []TaskStatus {
	p, err := s.GetProject(slug)
	if err != nil {
		return DefaultStatuses
	}
	return p.GetStatuses()
}

// GetLandingStatuses returns the statuses this project's executor moves a
// verify-green sub to (see Executor.LandingStatuses): "merged" and "pushed" by
// default, whatever the project configured otherwise. Consumers that need to
// know whether the executor is done with a task ask HERE instead of comparing
// against a hardcoded name - done_status/done_status_independent are
// configurable, and a hardcoded literal silently ignores them.
func (s *Store) GetLandingStatuses(slug string) []TaskStatus {
	p, err := s.GetProject(slug)
	if err != nil {
		return defaultExecutor().LandingStatuses()
	}
	return p.GetExecutor().LandingStatuses()
}

// GetAllLandingStatuses is GetLandingStatuses across every active project, for
// the cross-project views (pm_context unscoped, the board's ALL tab) that hold
// tasks from more than one project at once.
func (s *Store) GetAllLandingStatuses() []TaskStatus {
	projects, err := s.ListActiveProjects()
	if err != nil {
		return defaultExecutor().LandingStatuses()
	}
	var result []TaskStatus
	for _, slug := range projects {
		for _, st := range s.GetLandingStatuses(slug) {
			if !slices.Contains(result, st) {
				result = append(result, st)
			}
		}
	}
	return result
}

// GetAllStatuses returns the union of statuses across all active (non-archived) projects, preserving order.
// Default statuses come first, then any unique custom statuses.
func (s *Store) GetAllStatuses() []TaskStatus {
	projects, err := s.ListActiveProjects()
	if err != nil {
		return DefaultStatuses
	}

	seen := make(map[TaskStatus]bool)
	var result []TaskStatus
	// seed with defaults
	for _, s := range DefaultStatuses {
		seen[s] = true
		result = append(result, s)
	}
	for _, slug := range projects {
		for _, st := range s.GetProjectStatuses(slug) {
			if !seen[st] {
				seen[st] = true
				result = append(result, st)
			}
		}
	}
	return result
}

// CreateProject writes a new project.yaml, creating the project dir first.
// The lock is taken AFTER the dir exists (it lives inside that dir) and only
// covers the write - callers must not hold the project lock themselves.
//
// The slug check lives HERE, not only in pm_create_project: MkdirAll happily
// walks out of the pm root, so `pm projects add ../outside` created a
// project.yaml outside the data dir, and `pm projects add MyProj` created a
// project that `pm projects` lists but no command can address (ResolveProject
// lowercases the query, never the candidates) - with no `pm projects rm` to
// undo either. The MCP handler keeps its own pre-check on purpose: it also
// enforces the case-insensitive duplicate rule, which is a handler concern.
func (s *Store) CreateProject(slug string, p *Project) error {
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	// The prefix is the ID source for every auto-minted task, so an unsafe one
	// creates a project that cannot hold a task - the failure would only
	// surface later, on the first add, blaming an ID nobody typed.
	if p != nil {
		if err := ValidateProjectPrefix(p.Prefix); err != nil {
			return err
		}
		if err := ValidateGroup(p.Group); err != nil {
			return err
		}
	}
	dir := s.ProjectDir(slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if release, err := s.LockProject(slug); err == nil {
		defer release()
	} else {
		warnLockDegrade("creating project", slug, err)
	}
	return writeProject(s.ProjectYAML(slug), p)
}

func (s *Store) GetTasks(projectSlug string) ([]*Task, error) {
	dir := s.ProjectDir(projectSlug)
	tasks, err := ReadTasksFromDir(dir)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		t.Project = projectSlug
	}
	return tasks, nil
}

// GetAllTasks returns tasks from all active (non-archived) projects.
func (s *Store) GetAllTasks() ([]*Task, error) {
	projects, err := s.ListActiveProjects()
	if err != nil {
		return nil, err
	}

	var all []*Task
	for _, p := range projects {
		tasks, err := s.GetTasks(p)
		if err != nil {
			continue
		}
		all = append(all, tasks...)
	}
	return all, nil
}

// MoveTask changes a task's status and updates the timestamp.
// This is the single source of truth for status transition logic.
// Brief is preserved on all status transitions (useful for summaries/reverts).
//
// The caller's copy may be MINUTES old (a board loaded at the last reload, an
// epic sub read at run start) and a move rewrites the whole file - so the task
// is re-read FRESH under the project lock and only then moved, refreshing the
// caller's copy in place. Callers must NOT hold the project lock themselves
// (a second flock in the same process deadlocks); release before calling.
func (s *Store) MoveTask(t *Task, newStatus TaskStatus) error {
	// Validate BEFORE touching the file: a status outside the project's set
	// (a typo'd `pm mv`, an MCP call with another project's status) renders on
	// no board column - the task just vanishes until someone hand-edits the
	// file. AddTask already validates; the mutation path must too. Archived is
	// the one exception: it is system-level, never listed in project statuses.
	if newStatus != StatusArchived {
		if err := ValidateStatus(newStatus, s.GetProjectStatuses(t.Project)); err != nil {
			return err
		}
	}
	if release, err := s.LockProject(t.Project); err == nil {
		defer release()
	} else {
		warnLockDegrade("moving task "+t.Meta.ID, t.Project, err)
	}
	// The re-read is a PRECONDITION, not a best-effort refresh: swallowing its
	// error and writing the caller's copy anyway RESURRECTS a task another
	// session deleted in the meantime (the board holds card pointers from its
	// last reload and moves them from 8 call sites). Both failure classes -
	// the dir could not be read, or the task is not in it - mean the copy in
	// hand is unfit to write, so refuse and let the caller surface it.
	fresh, err := s.findByExactID(t.Project, t.Meta.ID)
	if err != nil {
		return fmt.Errorf("read project %s: %w", t.Project, err)
	}
	if fresh == nil {
		// Not necessarily a delete: ReadTasksFromDir also SKIPS a file whose
		// frontmatter stopped parsing, so name both causes rather than assert
		// the wrong one.
		return fmt.Errorf("task %s no longer exists or its file no longer parses", t.Meta.ID)
	}
	*t = *fresh
	// SetStatus, not a bare assignment: it stamps StatusChanged, and it
	// compares against the FRESH on-disk status, so a move to the status the
	// task already holds leaves the "stuck since" clock alone.
	t.SetStatus(newStatus)
	t.Meta.Updated = Now()
	return writeTask(t)
}

// WriteTask persists a task to disk. Use for in-place updates (reorder, undo, field changes).
// For new tasks, use AddTask. For status changes, use MoveTask.
func (s *Store) WriteTask(t *Task) error {
	return writeTask(t)
}

// UpdateProject persists project metadata to disk under the project lock.
//
// writeProject is itself a read-modify-write (it merges the fresh marshal into
// the existing file's node tree to keep comments and unknown keys), so its
// read->write window is WIDER than a task write's - and nothing serialized it
// before: two pm processes (parallel CC sessions' MCP servers, `pm executor
// init` racing the board) could interleave and drop each other's edits, the
// exact hazard LockProject documents for task files.
//
// This only covers the write itself. A caller that READS the project, edits a
// field and writes it back still has a lost-update window as wide as its own
// think time - use MutateProject for that. Callers must NOT hold the project
// lock themselves (a second flock in the same process deadlocks).
func (s *Store) UpdateProject(slug string, p *Project) error {
	if release, err := s.LockProject(slug); err == nil {
		defer release()
	} else {
		warnLockDegrade("updating project", slug, err)
	}
	return writeProject(s.ProjectYAML(slug), p)
}

// MutateProject serializes a whole read-modify-write on project.yaml: it takes
// the project lock, re-reads the project FRESH inside the critical section,
// applies fn to it and writes the result, returning the written project.
//
// This is the project-level counterpart of the FindTask -> mutate -> WriteTask
// pattern LockProject documents for tasks. Partial-field editors
// (pm_update_project, `pm executor init`) must go through it: reading the
// project, mutating that copy and calling UpdateProject leaves the caller's
// think time as a lost-update window, so a concurrent session's edit to an
// untouched field is silently reverted. fn must not call back into the store
// (no nested LockProject), and callers must not already hold the lock.
func (s *Store) MutateProject(slug string, fn func(*Project) error) (*Project, error) {
	if release, err := s.LockProject(slug); err == nil {
		defer release()
	} else {
		warnLockDegrade("mutating project", slug, err)
	}
	p, err := s.GetProject(slug)
	if err != nil {
		return nil, err
	}
	before, groupBefore := p.Prefix, p.Group
	if err := fn(p); err != nil {
		return nil, err
	}
	// Same CHANGE-scoped rule for the group: a hand-edited bad group must not
	// lock the project out of every other edit.
	if p.Group != groupBefore {
		if err := ValidateGroup(p.Group); err != nil {
			return nil, err
		}
	}
	// Reject a mutation that INTRODUCES an unsafe prefix (the ID source for
	// every auto-minted task - see CreateProject). Deliberately scoped to a
	// CHANGE: a project.yaml that already carries a bad prefix from before the
	// check must stay editable in every other field, or `pm executor init` and
	// pm_update_project would refuse to touch it at all.
	if p.Prefix != before {
		if err := ValidateProjectPrefix(p.Prefix); err != nil {
			return nil, err
		}
	}
	if err := writeProject(s.ProjectYAML(slug), p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) DeleteTask(t *Task) error {
	if t.FilePath == "" {
		return fmt.Errorf("task has no file path")
	}
	return os.Remove(t.FilePath)
}

// AddTask creates a task file for t under the project dir.
//
// It does NOT take the project lock. Minting an ID and writing it is a
// read-modify-write that must be serialized (see `pm add` / pm_add_task), but
// the lock has to cover NextTaskID too, so it belongs at the CALLER - and
// LockProject must never nest (a second flock on a fresh fd deadlocks against
// our own), so it cannot live in both places.
func (s *Store) AddTask(projectSlug string, t *Task) error {
	// The ID becomes the leading component of the file name and arrives from
	// outside pm on three paths (--id, MCP's id param, the board's add
	// prompt). Validate BEFORE the O_EXCL claim below, so a rejected ID leaves
	// no phantom file behind (same reason the mode/epic_mode pre-checks exist).
	if err := ValidateTaskID(t.Meta.ID); err != nil {
		// An auto-minted ID is "<prefix>-<n>", and a project.yaml predating the
		// prefix check (or hand-edited since) can still carry an unsafe one -
		// which surfaces here as an error about an ID the user never typed.
		// Name the actual culprit. ProjectPrefix falls back to the SLUG when
		// no prefix is set, so the two cases are worded apart: pointing at a
		// `prefix:` key that isn't in the file would send the reader hunting
		// for something that does not exist.
		if prefix := s.ProjectPrefix(projectSlug); prefix != "" &&
			strings.HasPrefix(t.Meta.ID, prefix) && ValidateTaskID(prefix) != nil {
			if proj, perr := s.GetProject(projectSlug); perr == nil && proj.Prefix != "" {
				return fmt.Errorf("%w - it was derived from the project's `prefix` %q, which is not a safe path component; fix it in %s",
					err, prefix, s.ProjectYAML(projectSlug))
			}
			return fmt.Errorf("%w - it was derived from the project directory name %q (no `prefix` is set); set a safe `prefix` in %s",
				err, prefix, s.ProjectYAML(projectSlug))
		}
		return err
	}

	t.Project = projectSlug
	t.FilePath = filepath.Join(s.ProjectDir(projectSlug), t.Filename())

	// Validate status against project's allowed statuses
	allowed := s.GetProjectStatuses(projectSlug)
	if err := ValidateStatus(t.Meta.Status, allowed); err != nil {
		return err
	}

	// O_EXCL: atomic create-or-fail, no race between stat and write
	f, err := os.OpenFile(t.FilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("task file already exists: %s", t.FilePath)
		}
		return err
	}
	f.Close()

	// File claimed; now write content atomically (temp+rename)
	if err := writeTask(t); err != nil {
		// writeTask rejected before touching the claimed file (e.g. mode/epic_mode
		// validation) - remove it so it doesn't linger as a 0-byte phantom task and
		// block a retry with "task file already exists". Only ever removes the file
		// THIS call just created via O_EXCL, never a pre-existing one.
		os.Remove(t.FilePath)
		return err
	}
	return nil
}

// ProjectPrefix returns the task ID prefix for a project.
// Uses project.yaml prefix field if set, otherwise falls back to slug.
func (s *Store) ProjectPrefix(slug string) string {
	proj, err := s.GetProject(slug)
	if err == nil && proj.Prefix != "" {
		return proj.Prefix
	}
	return slug
}

// NextTaskID returns the next sequential ID for a project (e.g. "orbit-3").
func (s *Store) NextTaskID(slug string) string {
	prefix := s.ProjectPrefix(slug)
	tasks, err := s.GetTasks(slug)
	if err != nil {
		return prefix + "-1"
	}

	maxN := 0
	for _, t := range tasks {
		if strings.HasPrefix(t.Meta.ID, prefix+"-") {
			numStr := strings.TrimPrefix(t.Meta.ID, prefix+"-")
			if n, err := strconv.Atoi(numStr); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("%s-%d", prefix, maxN+1)
}

// NextChildID returns the next sequential subtask ID under a parent
// (e.g. parent "atlas-39" -> "atlas-39-3"). Only direct children count -
// IDs whose remainder after the parent prefix is a plain integer.
func (s *Store) NextChildID(slug, parentID string) string {
	tasks, err := s.GetTasks(slug)
	if err != nil {
		return parentID + "-1"
	}
	prefix := parentID + "-"
	maxN := 0
	for _, t := range tasks {
		if !strings.HasPrefix(t.Meta.ID, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(t.Meta.ID, prefix)); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("%s-%d", parentID, maxN+1)
}

// ResolveProject finds a project slug by prefix match.
func (s *Store) ResolveProject(input string) (string, error) {
	folded := strings.ToLower(input)
	projects, err := s.ListProjects()
	if err != nil {
		return "", err
	}

	var matches []string
	for _, p := range projects {
		pFolded := strings.ToLower(p)
		if strings.HasPrefix(pFolded, folded) || pFolded == folded {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrProjectNotFound, input)
	case 1:
		return matches[0], nil
	default:
		if strings.ToLower(matches[0]) == folded {
			return matches[0], nil
		}
		return "", fmt.Errorf("ambiguous project %q: matches %s", input, strings.Join(matches, ", "))
	}
}

// FindTask finds a task by ID or title prefix within a project.
func (s *Store) FindTask(projectSlug, query string) (*Task, error) {
	tasks, err := s.GetTasks(projectSlug)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)

	// exact ID match first
	for _, t := range tasks {
		if strings.ToLower(t.Meta.ID) == query {
			return t, nil
		}
	}

	// prefix match on ID
	var idMatches []*Task
	for _, t := range tasks {
		if strings.HasPrefix(strings.ToLower(t.Meta.ID), query) {
			idMatches = append(idMatches, t)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0], nil
	}

	// title substring - deduplicated against ID matches so a task hitting
	// both phases is never counted twice in the ambiguity total.
	seen := make(map[string]bool, len(idMatches))
	matches := make([]*Task, len(idMatches))
	copy(matches, idMatches)
	for _, t := range idMatches {
		seen[t.Meta.ID] = true
	}
	for _, t := range tasks {
		if seen[t.Meta.ID] {
			continue
		}
		if strings.Contains(strings.ToLower(t.Meta.Title), query) {
			matches = append(matches, t)
			seen[t.Meta.ID] = true
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		// Wrapped sentinel (message unchanged): a caller scanning SEVERAL
		// projects must be able to tell "this project holds no candidate"
		// from "this project holds several" - collapsing both into a plain
		// error makes an ambiguous project contribute nothing, so the scan
		// happily resolves to some other project's task. See resolveWorkTask.
		return nil, fmt.Errorf("%w %q: %d matches", ErrAmbiguousTask, query, len(matches))
	}

	return nil, fmt.Errorf("%w: %q", ErrTaskNotFound, query)
}

// FindTaskExact finds a task by exact ID match only (case-insensitive) -
// no ID-prefix or title fallback. Use for irreversible operations (delete)
// where a fuzzy match could silently target the wrong task.
func (s *Store) FindTaskExact(projectSlug, taskID string) (*Task, error) {
	t, err := s.findByExactID(projectSlug, taskID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("%w: %q (exact task ID required, e.g. %q)", ErrTaskNotFound, taskID, s.ProjectPrefix(projectSlug)+"-1")
	}
	return t, nil
}

// findByExactID is the ONE place the exact-ID matching rule lives: the task
// whose ID equals taskID case-insensitively, or (nil, nil) when the project
// holds no such task. FindTaskExact and MoveTask's precondition re-read share
// it so the rule cannot drift; they differ only in how they word the miss.
func (s *Store) findByExactID(projectSlug, taskID string) (*Task, error) {
	tasks, err := s.GetTasks(projectSlug)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(taskID)
	for _, t := range tasks {
		if strings.ToLower(t.Meta.ID) == query {
			return t, nil
		}
	}
	return nil, nil
}
