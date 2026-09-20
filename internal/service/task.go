package service

import (
	"github.com/mbalazy/pm/internal/storage"
)

// --- Inputs (the MCP tool schemas are generated from these structs) ---

// AddTaskInput is the argument set of pm_add_task.
type AddTaskInput struct {
	Project    string            `json:"project" jsonschema:"Project slug or prefix"`
	Title      string            `json:"title" jsonschema:"Task title"`
	Status     string            `json:"status,omitempty" jsonschema:"Initial status (default: first project status)"`
	Branch     string            `json:"branch,omitempty" jsonschema:"Git branch name"`
	Parent     string            `json:"parent,omitempty" jsonschema:"Parent task ID for subtasks (e.g. atlas-39). Makes this a child of a tracker task."`
	Order      int               `json:"order,omitempty" jsonschema:"Sort order within a column / parent rollup (lower runs first; convention: 10, 20, 30...). 0 = unset (sorts before ordered siblings, then by ID). Used by pm run-epic for sub execution order."`
	DependsOn  []string          `json:"depends_on,omitempty" jsonschema:"Sub IDs this subtask depends on (e.g. atlas-39-2). pm run-epic skips this sub (no worker spawned) until every listed dep is merged/done, then a re-run picks it up. Empty = runs by Order."`
	Mode       string            `json:"mode,omitempty" jsonschema:"Execution mode for a subtask under pm run-epic. 'auto' (default, empty) runs autonomously via a headless worker. 'manual' marks it human-only: pm run-epic skips it entirely (no worker spawned, status untouched) as a PERMANENT gate on every run until you do the work and move it to the done status yourself. Use for subs needing interactive/visual work (simulator verification, design/visual checks)."`
	Model      string            `json:"model,omitempty" jsonschema:"Worker model override for this sub under pm run-epic / pm work (claude alias or full name, e.g. 'sonnet'). Empty = inherit the run-level model. Put trivial subs (copy/color/one-prop tweaks) on a cheaper model; leave investigation subs on the default."`
	EpicMode   string            `json:"epic_mode,omitempty" jsonschema:"How pm run-epic drives this PARENT tracker's subs. Empty (default) = integration mode: subs branch off and merge back into a shared epic/<tracker> branch, ending in one epic PR. 'independent' = batch mode for UNRELATED tasks: each sub gets its own branch off the base, is pushed when it carries commits, and nothing is merged (no integration branch, no epic PR). Meaningful on a tracker only."`
	FinishMode string            `json:"finish_mode,omitempty" jsonschema:"Whether pm run-epic chains the acceptance of this PARENT tracker itself. 'auto' = when the run is over, spawn a detached pm finish <tracker> (same machine only), so a batch launched at night is accepted by morning. 'off' or empty (default) = no chaining. Meaningful on a tracker only."`
	Runtime    string            `json:"runtime,omitempty" jsonschema:"Opt this task into the executor's runtime phase: after a green verify the worker drives the project's live runtime (simulator, browser) as bound by executor.phases.runtime and records readings as OBSERVED: lines with positive + negative controls. 'on' enables it; 'off' or empty (default) skips the phase. Set it ONLY on subs with a visible AC - the phase costs 10-20 turns plus screenshots and can read false; the rig check (executor.rig) runs once per run only when some sub has it on."`
	Tags       []string          `json:"tags,omitempty" jsonschema:"Tags"`
	Links      map[string]string `json:"links,omitempty" jsonschema:"Links as key=url pairs (e.g. azure, pr, slack)"`
	Body       string            `json:"body,omitempty" jsonschema:"Markdown body content. This is the append-only Log zone (session history)."`
	Spec       string            `json:"spec,omitempty" jsonschema:"Initial Spec block (current-truth zone): what we're building, current decisions, still-open questions. Wrapped in spec markers at the top of the body; the body field becomes the append-only Log below it."`
	ID         string            `json:"id,omitempty" jsonschema:"Task ID (auto-generated if omitted; when parent is set, auto-numbers as <parent>-<n>)"`
	Brief      string            `json:"brief,omitempty" jsonschema:"Short session context summary (overwrites previous)"`
	AC         string            `json:"ac,omitempty" jsonschema:"Acceptance criteria (overwrites previous)"`
	WaitingFor string            `json:"waiting_for,omitempty" jsonschema:"Who or what this task is blocked on - free text (e.g. 'review by the lead, PR #940', 'client answer'). Set it whenever you put a task on the waiting status, so the blocker is readable without opening the body."`
	Sessions   []string          `json:"sessions,omitempty" jsonschema:"Claude session IDs to attach"`
}

// UpdateTaskInput is the argument set of pm_update_task. Pointer fields are
// tri-state: nil = keep current, pointer to "" = clear.
type UpdateTaskInput struct {
	Project    string            `json:"project" jsonschema:"Project slug or prefix"`
	TaskID     string            `json:"task_id" jsonschema:"Task ID or search query"`
	Status     string            `json:"status,omitempty" jsonschema:"New status"`
	Title      string            `json:"title,omitempty" jsonschema:"New title. Required-non-empty: an empty/omitted value leaves the current title untouched (clearing a title is not a supported operation)."`
	Branch     *string           `json:"branch,omitempty" jsonschema:"Set the git branch name. Omit to keep current; pass an empty string to clear."`
	Parent     *string           `json:"parent,omitempty" jsonschema:"Set the parent task ID for subtasks (e.g. atlas-39), making this a child of a tracker task. Omit to keep current; pass an empty string to clear (de-parent)."`
	Order      *int              `json:"order,omitempty" jsonschema:"Set sort order within a column / parent rollup (lower runs first; convention: 10, 20, 30...). Omit to keep current; pass 0 to clear. Changes only the order - the rest of the task is untouched."`
	DependsOn  []string          `json:"depends_on,omitempty" jsonschema:"Replace the sub's depends_on list (sub IDs that must be merged/done before pm run-epic runs this sub). Omit to keep current; pass an empty array to clear."`
	Mode       *string           `json:"mode,omitempty" jsonschema:"Set the execution mode for pm run-epic. 'auto' runs the sub autonomously via a headless worker; 'manual' makes pm run-epic skip this sub (no worker spawned, status untouched) as a permanent gate until you do the work and move it to the done status yourself. Omit to keep current."`
	Model      *string           `json:"model,omitempty" jsonschema:"Set the worker model override for pm run-epic / pm work (claude alias or full name, e.g. 'sonnet'). Empty string clears it (inherit run-level model). Omit to keep current."`
	EpicMode   *string           `json:"epic_mode,omitempty" jsonschema:"Set how pm run-epic drives this PARENT tracker's subs. 'independent' = batch mode for UNRELATED tasks (each sub on its own branch off the base, pushed, nothing merged, no epic PR). Empty string clears it back to integration mode (shared epic/<tracker> branch, one epic PR). Omit to keep current."`
	FinishMode *string           `json:"finish_mode,omitempty" jsonschema:"Set whether pm run-epic chains the acceptance of this PARENT tracker itself. 'auto' = spawn a detached pm finish <tracker> when the run is over (same machine only). 'off' disables it; an empty string clears the field, which also means off. Omit to keep current."`
	Runtime    *string           `json:"runtime,omitempty" jsonschema:"Set whether this task runs the executor's runtime phase (worker drives the live runtime after verify, records OBSERVED: readings). 'on' enables it - only for subs with a visible AC; 'off' disables it; an empty string clears the field, which also means off. Omit to keep current."`
	Tags       []string          `json:"tags,omitempty" jsonschema:"Replace tags (omit to keep current)"`
	Links      map[string]string `json:"links,omitempty" jsonschema:"Links to merge (existing links are preserved)"`
	BodyAppend string            `json:"body_append,omitempty" jsonschema:"Append to the Log zone of the body (append-only session history; never replaces existing content)"`
	Spec       string            `json:"spec,omitempty" jsonschema:"Replace the current-truth Spec block (between <!-- spec:start --> / <!-- spec:end --> markers): what we're building, current decisions, still-open questions. Editable in place; created at the top of the body if absent. When a decision changes, rewrite the Spec to read as current truth AND append a one-line pointer to the Log via body_append (e.g. 'Q3 resolved -> see Spec'). Nothing is lost; the Spec never rots."`
	Brief      *string           `json:"brief,omitempty" jsonschema:"Set the short session context summary (overwrites previous). Omit to keep current; pass an empty string to clear."`
	AC         *string           `json:"ac,omitempty" jsonschema:"Set the acceptance criteria (overwrites previous). Omit to keep current; pass an empty string to clear."`
	WaitingFor *string           `json:"waiting_for,omitempty" jsonschema:"Set who or what this task is blocked on - free text (e.g. 'review by the lead, PR #940', 'client answer'). Set it whenever you move a task to the waiting status, and clear it when the block lifts. Omit to keep current; pass an empty string to clear."`
	Sessions   []string          `json:"sessions,omitempty" jsonschema:"Claude session IDs to append (never removes existing)"`
}

// MoveTaskInput is the argument set of pm_move_task.
type MoveTaskInput struct {
	Project   string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID    string `json:"task_id" jsonschema:"Task ID or search query"`
	NewStatus string `json:"new_status" jsonschema:"Target status"`
}

// DeleteTaskInput is the argument set of pm_delete_task.
type DeleteTaskInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID  string `json:"task_id" jsonschema:"Exact full task ID (e.g. my-app-3) - no fuzzy title match, this operation is irreversible"`
}

// --- Results ---

// MoveTaskResult reports a move. Field order is the JSON key order every
// existing client has seen (alphabetical, from the map it replaced).
type MoveTaskResult struct {
	ID        string `json:"id"`
	NewStatus string `json:"new_status"`
	OldStatus string `json:"old_status"`
}

// DeleteTaskResult names what was deleted.
type DeleteTaskResult struct {
	ID    string
	Title string
}

// --- Mutations ---

// AddTask creates a task in the project in.Project resolves to and returns it
// as written. Validation failures come back as *ValidationError.
func AddTask(store storage.TaskStore, in AddTaskInput) (*storage.Task, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	// Serialize the read-modify-write against other pm processes (parallel
	// CC sessions, executor managers) so concurrent updates never drop each
	// other; best-effort - a lock failure degrades to today's behaviour.
	if release, lockErr := store.LockProject(slug); lockErr == nil {
		defer release()
	}

	id := in.ID
	if id == "" {
		if in.Parent != "" {
			id = store.NextChildID(slug, in.Parent)
		} else {
			id = store.NextTaskID(slug)
		}
	}

	t := storage.NewTask(id, in.Title, slug)

	if in.Status != "" {
		t.SetStatus(storage.ParseStatus(in.Status))
	} else {
		statuses := store.GetProjectStatuses(slug)
		if len(statuses) > 0 {
			t.SetStatus(statuses[0])
		}
	}
	// SetStatus took its OWN clock read (it stamps mutations, and this is
	// still task creation), so realign the pair NewTask wrote from one
	// read. Without this, a task born on a non-default status can carry a
	// status_changed a second later than its updated - "the status moved
	// after the last edit", on a task nobody has edited yet.
	t.Meta.Updated = t.Meta.StatusChanged

	// Validate status (AddTask also validates, but fail early with a clear error)
	if err := validation(storage.ValidateStatus(t.Meta.Status, store.GetProjectStatuses(slug))); err != nil {
		return nil, err
	}

	// Validate mode (writeTask also validates, but fail early with a clear error)
	if err := validation(storage.ValidateMode(in.Mode)); err != nil {
		return nil, err
	}

	// Validate epic_mode. writeTask validates too, but this pre-check is
	// REQUIRED, not just a nicer error: AddTask claims the file with O_EXCL
	// before writeTask runs, so a validation failure down there would leave a
	// 0-byte task file behind and make the retry fail as "already exists".
	if err := validation(storage.ValidateEpicMode(in.EpicMode)); err != nil {
		return nil, err
	}

	// finish_mode: same load-bearing pre-check as epic_mode above, for the
	// same O_EXCL reason - not a nicer error message.
	if err := validation(storage.ValidateFinishMode(in.FinishMode)); err != nil {
		return nil, err
	}
	if err := validation(storage.ValidateRuntime(in.Runtime)); err != nil {
		return nil, err
	}

	t.Meta.Branch = in.Branch
	t.Meta.Parent = in.Parent
	t.Meta.Order = in.Order
	t.Meta.DependsOn = in.DependsOn
	t.Meta.Mode = in.Mode
	t.Meta.Model = in.Model
	t.Meta.EpicMode = in.EpicMode
	t.Meta.FinishMode = in.FinishMode
	t.Meta.Runtime = in.Runtime
	t.Meta.Tags = in.Tags
	if len(in.Links) > 0 {
		t.Meta.Links = in.Links
	}
	t.Body = in.Body
	if in.Spec != "" {
		t.Body = storage.ApplySpec(t.Body, in.Spec)
	}
	t.Meta.Brief = in.Brief
	t.Meta.AC = in.AC
	t.Meta.WaitingFor = in.WaitingFor
	t.Meta.Sessions = in.Sessions

	if err := store.AddTask(slug, t); err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateTask patches the fields the caller passed and writes the task, under
// the project lock with the task read FRESH inside it. Links merge, tags
// replace, body_append goes to the Log, spec rewrites the Spec block,
// sessions append; the pointer fields are tri-state and title is never
// cleared. A status change is stamped via SetStatus.
func UpdateTask(store storage.TaskStore, in UpdateTaskInput) (*storage.Task, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	// Serialize the read-modify-write against other pm processes (parallel
	// CC sessions, executor managers) so concurrent updates never drop each
	// other; best-effort - a lock failure degrades to today's behaviour.
	if release, lockErr := store.LockProject(slug); lockErr == nil {
		defer release()
	}
	task, err := store.FindTask(slug, in.TaskID)
	if err != nil {
		return nil, err
	}

	if in.Status != "" {
		// Same rule as MoveTask: an unlisted status renders on no board column
		// (the task vanishes), so reject it here; archived is system-level and
		// always legal.
		st := storage.ParseStatus(in.Status)
		if err := validateStatusForProject(store, slug, st); err != nil {
			return nil, err
		}
		// SetStatus, not a bare assignment: this path writes the task itself
		// (no MoveTask), so it is the second and last place that has to
		// stamp status_changed.
		task.SetStatus(st)
	}
	if in.Title != "" {
		task.Meta.Title = in.Title
	}
	if in.Branch != nil {
		task.Meta.Branch = *in.Branch
	}
	if in.Parent != nil {
		task.Meta.Parent = *in.Parent
	}
	if in.Order != nil {
		task.Meta.Order = *in.Order
	}
	if in.DependsOn != nil {
		task.Meta.DependsOn = in.DependsOn
	}
	if in.Mode != nil {
		if err := validation(storage.ValidateMode(*in.Mode)); err != nil {
			return nil, err
		}
		task.Meta.Mode = *in.Mode
	}
	if in.Model != nil {
		task.Meta.Model = *in.Model
	}
	if in.EpicMode != nil {
		if err := validation(storage.ValidateEpicMode(*in.EpicMode)); err != nil {
			return nil, err
		}
		task.Meta.EpicMode = *in.EpicMode
	}
	if in.FinishMode != nil {
		if err := validation(storage.ValidateFinishMode(*in.FinishMode)); err != nil {
			return nil, err
		}
		task.Meta.FinishMode = *in.FinishMode
	}
	if in.Runtime != nil {
		if err := validation(storage.ValidateRuntime(*in.Runtime)); err != nil {
			return nil, err
		}
		task.Meta.Runtime = *in.Runtime
	}
	if in.Tags != nil {
		task.Meta.Tags = in.Tags
	}

	// Links: merge, never remove
	if len(in.Links) > 0 {
		if task.Meta.Links == nil {
			task.Meta.Links = make(map[string]string)
		}
		for k, v := range in.Links {
			task.Meta.Links[k] = v
		}
	}

	// Spec: replace the current-truth Spec block in place (the Log zone,
	// everything outside the markers, is left untouched).
	if in.Spec != "" {
		task.Body = storage.ApplySpec(task.Body, in.Spec)
	}

	// Body: append to the Log, never replace
	if in.BodyAppend != "" {
		if task.Body != "" {
			task.Body = task.Body + "\n\n" + in.BodyAppend
		} else {
			task.Body = in.BodyAppend
		}
	}

	// Brief: overwrite (current state, not history). Tri-state: omit keeps
	// current, empty string clears (matches Mode/Model/EpicMode).
	if in.Brief != nil {
		task.Meta.Brief = *in.Brief
	}

	// AC: overwrite (acceptance criteria, not history). Tri-state: omit keeps
	// current, empty string clears.
	if in.AC != nil {
		task.Meta.AC = *in.AC
	}

	// WaitingFor: overwrite (the current blocker, not history). Tri-state:
	// omit keeps current, empty string clears - the block lifted.
	if in.WaitingFor != nil {
		task.Meta.WaitingFor = *in.WaitingFor
	}

	// Sessions: append, never remove
	if len(in.Sessions) > 0 {
		task.Meta.Sessions = append(task.Meta.Sessions, in.Sessions...)
	}

	task.Meta.Updated = storage.Now()
	if err := store.WriteTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

// MoveTask moves a task to in.NewStatus through Store.MoveTask, which locks
// the project itself and re-reads the task fresh - so no lock is taken here
// (a second flock in-process would deadlock). The status is checked up front
// with the same rule MoveTask applies (archived always legal), only so the
// failure is typed as a *ValidationError.
func MoveTask(store storage.TaskStore, in MoveTaskInput) (*MoveTaskResult, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	task, err := store.FindTask(slug, in.TaskID)
	if err != nil {
		return nil, err
	}

	oldStatus := string(task.Meta.Status)
	newStatus := storage.ParseStatus(in.NewStatus)
	if err := validateStatusForProject(store, slug, newStatus); err != nil {
		return nil, err
	}
	if err := store.MoveTask(task, newStatus); err != nil {
		return nil, err
	}
	return &MoveTaskResult{
		ID:        task.Meta.ID,
		OldStatus: oldStatus,
		NewStatus: string(task.Meta.Status),
	}, nil
}

// DeleteTask removes the task file for the EXACT id in.TaskID - the one
// irreversible mutation, so FindTask's fuzzy prefix/title resolution is
// deliberately not used (a title query errors with nothing deleted).
func DeleteTask(store storage.TaskStore, in DeleteTaskInput) (*DeleteTaskResult, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	// Serialize against other pm processes so a concurrent update never
	// resurrects the file mid-delete; best-effort, like the other mutations.
	if release, lockErr := store.LockProject(slug); lockErr == nil {
		defer release()
	}
	task, err := store.FindTaskExact(slug, in.TaskID)
	if err != nil {
		return nil, err
	}

	res := &DeleteTaskResult{ID: task.Meta.ID, Title: task.Meta.Title}
	if err := store.DeleteTask(task); err != nil {
		return nil, err
	}
	return res, nil
}
