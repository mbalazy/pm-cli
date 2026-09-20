import type { TaskSummary } from '../api/types'

export interface StatusGroup {
  status: string
  tasks: TaskSummary[]
}

/**
 * Groups tasks into the project's status columns, in the project's order, and
 * orders each column the way the board does - a port of Go's
 * `storage.LessByOrder`, the one comparator the TUI columns, the tracker rollup
 * and the TUI child list share: `order` ascending (0 = unset sorts FIRST, as in
 * Go), then the numeric ID suffix ascending, then `updated` descending as the
 * raw stamp string. Keep it in step with LessByOrder. A task whose
 * status is not in `statuses` (a project.yaml edited under it) still shows up,
 * in a trailing group of its own - never silently dropped. Every listed status
 * gets a group, empty or not, so the columns are stable across projects.
 */
export function groupByStatus(tasks: TaskSummary[], statuses: string[]): StatusGroup[] {
  const groups = new Map<string, TaskSummary[]>()
  for (const s of statuses) groups.set(s, [])
  for (const t of tasks) {
    const g = groups.get(t.status)
    if (g) g.push(t)
    else groups.set(t.status, [t])
  }
  return [...groups.entries()].map(([status, ts]) => ({
    status,
    tasks: [...ts].sort(compareTasks),
  }))
}

function compareTasks(a: TaskSummary, b: TaskSummary): number {
  const ao = a.order ?? 0
  const bo = b.order ?? 0
  if (ao !== bo) return ao - bo
  const na = taskIdNum(a.id)
  const nb = taskIdNum(b.id)
  if (na !== nb) return na - nb
  if (a.updated !== b.updated) return a.updated > b.updated ? -1 : 1
  return 0
}

/** Go's `taskIDNum`: the trailing integer of a task id ("orbit-26" -> 26), 0 when absent. */
function taskIdNum(id: string): number {
  const i = id.lastIndexOf('-')
  if (i < 0) return 0
  const n = Number(id.slice(i + 1))
  return Number.isInteger(n) && id.slice(i + 1) !== '' ? n : 0
}

/** Sum of a project's non-archived task counts - the sidebar badge. */
export function openTaskCount(counts: Record<string, number>): number {
  let n = 0
  for (const [status, c] of Object.entries(counts)) {
    if (status !== 'archived') n += c
  }
  return n
}
