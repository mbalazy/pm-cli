import type { TaskSummary } from '../api/types'

export interface StatusGroup {
  status: string
  tasks: TaskSummary[]
}

/**
 * Groups tasks into the project's status columns, in the project's order, and
 * orders each column the way the board does: `order` ascending (0 = unset,
 * sorts last), then `updated` descending as the raw stamp string. A task whose
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
  if (ao !== bo) {
    if (ao === 0) return 1
    if (bo === 0) return -1
    return ao - bo
  }
  if (a.updated !== b.updated) return a.updated > b.updated ? -1 : 1
  return 0
}

/** Sum of a project's non-archived task counts - the sidebar badge. */
export function openTaskCount(counts: Record<string, number>): number {
  let n = 0
  for (const [status, c] of Object.entries(counts)) {
    if (status !== 'archived') n += c
  }
  return n
}
