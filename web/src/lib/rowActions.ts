import type { AttentionRow } from '../api/types'

// The closed action vocabulary of an attention row (storage.AttentionRow
// .actions). The API decides WHICH actions a row carries; this table only
// says how each one is worded and whether this build can perform it yet -
// the mutation subs flip `pending` as they land (batch 1, pm-cli-118-16, is
// in; sleep_project came with the settings screen, pm-cli-118-18; run
// control - claim, release_claim, rerun_finish, resume_run, kill - with
// pm-cli-118-21, each through the dialog with the argv preview).

export type RowAction =
  | 'open'
  | 'report'
  | 'claim'
  | 'rerun_finish'
  | 'resume_run'
  | 'kill'
  | 'focus_toggle'
  | 'set_waiting_for'
  | 'back_to_todo'
  | 'mark_seen'
  | 'sleep_project'
  | 'open_pr'
  | 'release_claim'

export interface ActionMeta {
  label: string
  /** Empty = works now; otherwise the sub that wires it (shown as the disabled title). */
  pending: string
}

const META: Record<RowAction, ActionMeta> = {
  open: { label: 'open', pending: '' },
  report: { label: 'report', pending: 'acceptance report view: later' },
  claim: { label: 'claim', pending: '' },
  rerun_finish: { label: 'rerun acceptance', pending: '' },
  resume_run: { label: 'resume run', pending: '' },
  kill: { label: 'kill', pending: '' },
  focus_toggle: { label: 'focus', pending: '' },
  set_waiting_for: { label: 'waiting…', pending: '' },
  back_to_todo: { label: 'back to todo', pending: '' },
  mark_seen: { label: 'seen', pending: '' },
  sleep_project: { label: 'sleep', pending: '' },
  open_pr: { label: 'PR', pending: 'needs the task link' },
  release_claim: { label: 'release claim', pending: '' },
}

export function actionMeta(action: string): ActionMeta {
  return META[action as RowAction] ?? { label: action, pending: 'unknown action' }
}

/** Where `open` goes: the task detail, or the project board for a project row. */
export type OpenTarget =
  | { to: '/p/$slug/t/$id'; params: { slug: string; id: string } }
  | { to: '/p/$slug'; params: { slug: string } }

export function openTarget(row: Pick<AttentionRow, 'project' | 'task_id'>): OpenTarget {
  if (row.task_id) return { to: '/p/$slug/t/$id', params: { slug: row.project, id: row.task_id } }
  return { to: '/p/$slug', params: { slug: row.project } }
}
