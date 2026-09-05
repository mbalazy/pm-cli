import type { ChangeEvent, Project, RunRow, Tracker } from '../api/types'
import { runKey } from './runsView'

// The group page: which repos a group has, which tab the URL names, and the
// small joins its tables need. Groups themselves are the API's (`group` on
// every /api/projects row); this only reads them.

export type GroupTab = 'overview' | 'board' | 'runs' | 'changes'
export const GROUP_TABS: readonly GroupTab[] = ['overview', 'board', 'runs', 'changes']

export function parseTab(v: unknown): GroupTab {
  return typeof v === 'string' && (GROUP_TABS as readonly string[]).includes(v)
    ? (v as GroupTab)
    : 'overview'
}

/** The tab `delta` steps away, wrapping (the [ / ] keys). */
export function stepTab(tab: GroupTab, delta: number): GroupTab {
  const i = GROUP_TABS.indexOf(tab)
  return GROUP_TABS[(i + delta + GROUP_TABS.length) % GROUP_TABS.length]
}

/** The group slug of a project (its own slug when it names none). */
export function groupOf(projects: Project[] | undefined, slug: string): string {
  const p = projects?.find((x) => x.slug === slug)
  return p?.group || slug
}

/** The active member projects of a group, in the API's order. */
export function groupMembers(projects: Project[] | undefined, group: string): Project[] {
  return (projects ?? []).filter((p) => !p.archived && (p.group || p.slug) === group)
}

/** The group's display name: the first member's group_name, else the slug. */
export function groupName(members: Project[], group: string): string {
  return members[0]?.group_name || members[0]?.name || group
}

/** The repo the board tab shows: the URL's when it is a member, else the first member. */
export function activeRepo(members: Project[], wanted: string | undefined): string {
  if (wanted && members.some((m) => m.slug === wanted)) return wanted
  return members[0]?.slug ?? ''
}

export interface TrackerRow {
  project: string
  tracker: Tracker
  /** The matching runs row, when the tracker has one. */
  run?: RunRow
}

/** Every member's trackers, in member order, each joined with its run row. */
export function trackerRows(
  members: string[],
  trackers: (Tracker[] | undefined)[],
  runs: RunRow[] | undefined,
): TrackerRow[] {
  const byKey = new Map<string, RunRow>()
  for (const r of runs ?? []) if (!r.remote) byKey.set(runKey(r), r)
  const out: TrackerRow[] = []
  members.forEach((project, i) => {
    for (const tracker of trackers[i] ?? []) {
      out.push({ project, tracker, run: byKey.get(`/${project}/${tracker.id}`) })
    }
  })
  return out
}

/** The feed's events of one group, in the feed's order. */
export function filterChanges(events: ChangeEvent[] | undefined, group: string): ChangeEvent[] {
  return (events ?? []).filter((e) => e.group === group)
}
