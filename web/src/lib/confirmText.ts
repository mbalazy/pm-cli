import type { MutationRequest } from '../api/mutations'
import type { RunFlags, RunPlan } from '../api/types'

// Every mutation goes through one confirmation dialog (the user's rule: "I do
// not want to fire something by accident"). This file decides, for a pending
// action, what the dialog SAYS (heading, project, the one sentence of what
// will happen, whether a text field is needed and which warning to show) and
// what it SENDS on confirm. Pure; the dialog component only renders it.

/** The subject of an action: an attention row, or a task detail. */
export interface ActionSubject {
  project: string
  task_id?: string
  title: string
  status?: string
  /** Current values, pre-filling the field of an edit. */
  waiting_for?: string
  brief?: string
  /** A changes row's stamp - mark_seen marks everything up to it. */
  since?: string
  /** A project's notes ("where we left off"), for edit_notes. */
  notes?: string
  /** move_repo: the group the project moves to ('' = its own). */
  group?: string
}

/** A task detail as an action subject (the detail carries `id`, a row `task_id`). */
export function subjectOfTask(t: {
  project: string
  id: string
  title: string
  status: string
  waiting_for?: string
  brief?: string
}): ActionSubject {
  return {
    project: t.project,
    task_id: t.id,
    title: t.title,
    status: t.status,
    waiting_for: t.waiting_for,
    brief: t.brief,
  }
}

export type PendingKind =
  | 'focus_toggle'
  | 'set_waiting_for'
  | 'back_to_todo'
  | 'mark_seen'
  | 'edit_brief'
  | 'edit_waiting_for'
  | 'set_status'
  | 'edit_notes'
  | 'sleep_project'
  | 'wake_project'
  | 'move_repo'
  | RunKind

/** The run-control kinds (pm-cli-118-21): a POST /api/runs/{p}/{id}/{action}. */
export type RunKind = 'claim' | 'release_claim' | 'rerun_finish' | 'resume_run' | 'kill'

export function isRunKind(k: string): k is RunKind {
  return (
    k === 'claim' ||
    k === 'release_claim' ||
    k === 'rerun_finish' ||
    k === 'resume_run' ||
    k === 'kill'
  )
}

export interface PendingAction {
  kind: PendingKind
  subject: ActionSubject
  /** set_status: the status chosen in the select. */
  status?: string
}

/** One launch flag the dialog offers as a checkbox. */
export interface FlagOption {
  key: keyof RunFlags
  label: string
  /** A flag the project cannot honour is shown disabled with this note. */
  disabled?: string
}

export interface ConfirmField {
  kind: 'input' | 'textarea'
  label: string
  placeholder: string
}

export interface ConfirmText {
  heading: string
  project: string
  sentence: string
  confirmLabel: string
  field?: ConfirmField
  /** Shown under the field when the current value deserves a second look. */
  warning?: string
  /** A run action's exact command, as the server would run it (the board's overlay rule). */
  preview?: string
  /** The server's launch warnings: a live run, a dirty tree, a held claim. Shown, never blocking. */
  warnings?: string[]
  /** The checkboxes a run action offers. */
  flags?: FlagOption[]
  /** True while the preview is still loading - the confirm button waits for it. */
  loading?: boolean
}

/** The kinds this build knows; anything else has no dialog and no request. */
export function isPendingKind(k: string): k is PendingKind {
  return (
    k === 'focus_toggle' ||
    k === 'set_waiting_for' ||
    k === 'back_to_todo' ||
    k === 'mark_seen' ||
    k === 'edit_brief' ||
    k === 'edit_waiting_for' ||
    k === 'set_status' ||
    k === 'edit_notes' ||
    k === 'sleep_project' ||
    k === 'wake_project' ||
    k === 'move_repo' ||
    isRunKind(k)
  )
}

/** The field's starting value for an action that edits one. */
export function initialValue(p: PendingAction): string {
  switch (p.kind) {
    case 'edit_brief':
      return p.subject.brief ?? ''
    case 'edit_notes':
      return p.subject.notes ?? ''
    case 'edit_waiting_for':
    case 'set_waiting_for':
      return p.subject.waiting_for ?? ''
    case 'set_status':
      return p.status === 'waiting' ? (p.subject.waiting_for ?? '') : ''
    default:
      return ''
  }
}

const NO_REASON = 'no reason given - the row will show as an alarm (no reason) on the home screen'

/** Which flags each run action offers (the server ignores the rest). */
export function flagOptions(kind: RunKind, plan?: RunPlan): FlagOption[] {
  const additional: FlagOption = {
    key: 'additional',
    label: 'in an additional worktree slot (--additional)',
    disabled:
      plan && !plan.additional_avail ? 'no worktree slots configured for this project' : undefined,
  }
  switch (kind) {
    case 'resume_run':
      return [{ key: 'yolo', label: 'bypass permission prompts (--yolo)' }, additional]
    case 'rerun_finish':
      return [additional]
    default:
      return []
  }
}

/** The argv preview line of a plan: `cd <cwd> && pm ...`, or the claim/kill target. */
export function previewOf(plan: RunPlan | undefined): string {
  if (!plan) return ''
  if (plan.argv && plan.argv.length > 0) {
    const cmd = ['pm', ...plan.argv].join(' ')
    return plan.cwd ? `cd ${plan.cwd} && ${cmd}` : cmd
  }
  return plan.target ?? ''
}

export function describeAction(
  p: PendingAction,
  value: string,
  focused?: boolean,
  plan?: RunPlan,
): ConfirmText {
  const s = p.subject
  const id = s.task_id ?? s.project
  const heading = s.task_id ? `${s.task_id} ${s.title}` : s.title
  const base = { heading, project: s.project }
  if (isRunKind(p.kind)) return describeRun(p.kind, id, base, plan)
  switch (p.kind) {
    case 'focus_toggle':
      return {
        ...base,
        sentence: focused
          ? `Drops ${id} from today's focus.`
          : `Puts ${id} on today's focus (focus.yaml).`,
        confirmLabel: focused ? 'drop from focus' : 'add to focus',
      }
    case 'set_waiting_for':
      return {
        ...base,
        sentence: `Moves ${id} to waiting${value.trim() ? ` with the reason: ${value.trim()}` : ''}.`,
        confirmLabel: 'move to waiting',
        field: { kind: 'input', label: 'waiting for', placeholder: 'who or what blocks this' },
        warning: value.trim() ? undefined : NO_REASON,
      }
    case 'back_to_todo':
      return {
        ...base,
        sentence: `Moves ${id} back to todo and clears its waiting reason.`,
        confirmLabel: 'back to todo',
      }
    case 'mark_seen':
      return {
        ...base,
        sentence: s.since
          ? `Marks every change up to this one (${s.since}) as seen.`
          : 'Marks every change so far as seen.',
        confirmLabel: 'mark seen',
      }
    case 'edit_brief':
      return {
        ...base,
        sentence: `Replaces the brief of ${id} (overwrite, like pm_update_task).`,
        confirmLabel: 'save brief',
        field: { kind: 'textarea', label: 'brief', placeholder: 'where we left off' },
        warning: value.trim() ? undefined : 'an empty brief clears it',
      }
    case 'edit_waiting_for':
      return {
        ...base,
        sentence: value.trim()
          ? `Sets the waiting reason of ${id}.`
          : `Clears the waiting reason of ${id}.`,
        confirmLabel: 'save reason',
        field: { kind: 'input', label: 'waiting for', placeholder: 'who or what blocks this' },
        warning: s.status === 'waiting' && !value.trim() ? NO_REASON : undefined,
      }
    case 'edit_notes':
      return {
        ...base,
        sentence: `Replaces the notes of project ${s.project} ("where we left off"; an empty text keeps the current notes - the API treats empty as "leave").`,
        confirmLabel: 'save notes',
        field: { kind: 'textarea', label: 'notes', placeholder: 'where we left off' },
        warning: value.trim()
          ? undefined
          : 'empty notes are NOT saved - the API keeps the current text',
      }
    case 'set_status': {
      const to = p.status ?? ''
      const waiting = to === 'waiting'
      return {
        ...base,
        sentence: `Moves ${id} from ${s.status ?? '?'} to ${to}${waiting && value.trim() ? ` with the reason: ${value.trim()}` : ''}.`,
        confirmLabel: `move to ${to}`,
        field: waiting
          ? { kind: 'input', label: 'waiting for', placeholder: 'who or what blocks this' }
          : undefined,
        warning: waiting && !value.trim() ? NO_REASON : undefined,
      }
    }
    case 'sleep_project':
      return {
        ...base,
        sentence: `Puts project ${s.project} to sleep (archived: true in project.yaml).`,
        confirmLabel: 'sleep',
        warning:
          'an asleep project leaves every group, the home queue, the sidebar and the change feed until woken here',
      }
    case 'wake_project':
      return {
        ...base,
        sentence: `Wakes project ${s.project} (archived: false) - it rejoins its group and the home queue.`,
        confirmLabel: 'wake',
      }
    case 'move_repo':
      return {
        ...base,
        sentence: s.group
          ? `Moves ${s.project} into group ${s.group} (group: ${s.group} in its project.yaml).`
          : `Takes ${s.project} out of its group - it becomes a group of its own.`,
        confirmLabel: s.group ? `move to ${s.group}` : 'leave group',
      }
  }
}

function describeRun(
  kind: RunKind,
  id: string,
  base: { heading: string; project: string },
  plan: RunPlan | undefined,
): ConfirmText {
  const common = {
    ...base,
    preview: previewOf(plan),
    warnings: plan?.warnings,
    flags: flagOptions(kind, plan),
    loading: plan === undefined,
  }
  switch (kind) {
    case 'claim':
      return {
        ...common,
        sentence: `Claims the acceptance of ${id} for this cockpit${plan?.session ? ` (session ${plan.session})` : ''}. pm serve refreshes the claim every 3 minutes for at most an hour; release it when done, or let it lapse.`,
        confirmLabel: 'claim',
      }
    case 'release_claim':
      return {
        ...common,
        sentence: `Releases the cockpit's acceptance claim on ${id}, so another session can take the run.`,
        confirmLabel: 'release claim',
      }
    case 'rerun_finish':
      return {
        ...common,
        sentence: `Starts a detached acceptance of ${id} (pm finish, --yolo by default, never --sim: nobody may be at the screen). It refuses on a held claim.`,
        confirmLabel: 'start acceptance',
      }
    case 'resume_run':
      return {
        ...common,
        sentence: `Starts a detached run of ${id} (re-entrant: finished subs are skipped). Its output goes to the log the preview names.`,
        confirmLabel: 'start run',
      }
    case 'kill':
      return {
        ...common,
        sentence: plan?.pid
          ? `Sends SIGTERM to ${plan.target}, SIGKILL two seconds later if it survives; the in-flight sub is parked on waiting.`
          : `Nothing of ${id} is running - the kill will report the run as already gone.`,
        confirmLabel: 'kill',
      }
  }
}

/** The request a confirmed action sends. */
export function requestFor(p: PendingAction, value: string, flags: RunFlags = {}): MutationRequest {
  const s = p.subject
  const taskId = s.task_id ?? ''
  const v = value.trim()
  if (isRunKind(p.kind)) {
    return { kind: 'run', project: s.project, taskId, action: p.kind, flags }
  }
  switch (p.kind) {
    case 'focus_toggle':
      return { kind: 'focus', project: s.project, taskId }
    case 'set_waiting_for':
      return {
        kind: 'task',
        project: s.project,
        taskId,
        body: { status: 'waiting', waiting_for: v },
      }
    case 'back_to_todo':
      return { kind: 'task', project: s.project, taskId, body: { status: 'todo', waiting_for: '' } }
    case 'mark_seen':
      return { kind: 'seen', ts: s.since }
    case 'edit_brief':
      return { kind: 'task', project: s.project, taskId, body: { brief: value } }
    case 'edit_waiting_for':
      return { kind: 'task', project: s.project, taskId, body: { waiting_for: v } }
    case 'edit_notes':
      return { kind: 'project', project: s.project, body: { notes: value } }
    case 'sleep_project':
      return { kind: 'project', project: s.project, body: { archived: true } }
    case 'wake_project':
      return { kind: 'project', project: s.project, body: { archived: false } }
    case 'move_repo':
      return { kind: 'project', project: s.project, body: { group: s.group ?? '' } }
    case 'set_status':
      return {
        kind: 'task',
        project: s.project,
        taskId,
        body: p.status === 'waiting' ? { status: 'waiting', waiting_for: v } : { status: p.status },
      }
  }
}
