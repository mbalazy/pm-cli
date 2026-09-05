import type { TaskSummary, Tracker } from '../api/types'
import { parseStamp } from './relativeTime'

// The group page's "in progress" list: doing tasks of every member repo, the
// freshest activity first. Activity = the later of `updated` and
// `status_changed` (the STUCK rule's measure - "did anything at all happen
// here"); the API has no such field yet, so it is derived here, in lib, with
// a test - never in a component.

/** The task's last activity as a Date; null when neither stamp parses. */
export function activityOf(t: Pick<TaskSummary, 'updated' | 'status_changed'>): Date | null {
  const a = parseStamp(t.updated)
  const b = t.status_changed ? parseStamp(t.status_changed) : null
  if (a && b) return a > b ? a : b
  return a ?? b
}

/** Doing tasks of the given lists, merged, freshest activity first (unknown last). */
export function doingByActivity(lists: TaskSummary[][]): TaskSummary[] {
  const all = lists.flat().filter((t) => t.status === 'doing')
  return all
    .map((t, i) => ({ t, i, at: activityOf(t)?.getTime() ?? Number.NEGATIVE_INFINITY }))
    .sort((x, y) => (x.at !== y.at ? y.at - x.at : x.i - y.i))
    .map((x) => x.t)
}

/** True when the task has had no activity for `idleDays` or more (cockpit.doing_idle_days). */
export function isIdle(
  t: Pick<TaskSummary, 'updated' | 'status_changed'>,
  idleDays: number,
  now: Date = new Date(),
): boolean {
  const at = activityOf(t)
  if (!at) return true
  return now.getTime() - at.getTime() >= idleDays * 86_400_000
}

/** Seconds since the task's last activity; null when unknown. */
export function activityAgeSeconds(
  t: Pick<TaskSummary, 'updated' | 'status_changed'>,
  now: Date = new Date(),
): number | null {
  const at = activityOf(t)
  return at ? Math.max(0, Math.floor((now.getTime() - at.getTime()) / 1000)) : null
}

/** Tracker rollups by id, for the "3/5" next to a doing tracker. */
export function trackerIndex(lists: (Tracker[] | undefined)[]): Map<string, Tracker> {
  const m = new Map<string, Tracker>()
  for (const l of lists) for (const tr of l ?? []) m.set(tr.id, tr)
  return m
}

/** "3 merged · 1 done" - the rollup's progress map as text, statuses in the map's order. */
export function progressText(tr: Pick<Tracker, 'progress' | 'total'>): string {
  const parts = Object.entries(tr.progress ?? {}).map(([status, n]) => `${n} ${status}`)
  return parts.length === 0 ? `0/${tr.total}` : parts.join(' · ')
}
