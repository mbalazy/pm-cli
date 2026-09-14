import type { AttentionRow, AttentionSection, DismissRow } from '../api/types'

// The home screen's section vocabulary: a title, one sentence saying how the
// section is computed (the wireframe's "why" line) and the sentence shown
// when it is empty. Keyed by the API's section name; an unknown name falls
// back to the name itself so a new backend section renders before this file
// learns its words. The ORDER and MEMBERSHIP of sections are the API's.

export interface SectionMeta {
  title: string
  why: string
  empty: string
}

const META: Record<string, SectionMeta> = {
  needs_me: {
    title: 'Needs me',
    why: 'worst first: failed › visual claims › landed without acceptance › partial › live claim',
    empty: 'Nothing needs you.',
  },
  solo_reports: {
    title: 'Solo reports',
    why: 'closed /solo shifts of the last 14 days · dismiss = read',
    empty: 'No solo report to read.',
  },
  landed_no_pr: {
    title: 'Accepted, no PR',
    why: 'subs accepted by the executor whose PR is still to be opened',
    empty: 'No accepted work is waiting for a PR.',
  },
  focus: {
    title: "Today's focus",
    why: 'focus.yaml, your order · t = drop from focus',
    empty: 'No focus plan for today.',
  },
  in_progress: {
    title: 'In progress',
    why: 'live runs: phase · heartbeat · elapsed. Never the transcript.',
    empty: 'Nothing is running.',
  },
  waiting: {
    title: 'Waiting on',
    why: 'oldest first · highlighted past the threshold · no reason = alarm',
    empty: 'Nothing is waiting on anyone.',
  },
  changes: {
    title: 'Changes since the cutoff',
    why: 'a digest · the full feed lives on the Changes screen',
    empty: 'Nothing changed since the cutoff.',
  },
  stuck_projects: {
    title: 'Stuck projects',
    why: 'an active project with no task change for the threshold, or with doing tasks idle past theirs',
    empty: 'No project is stuck.',
  },
  new_since_cutoff: {
    title: 'New since the cutoff',
    why: 'tasks created after the cutoff',
    empty: 'Nothing new since the cutoff.',
  },
  recent: {
    title: 'Recently touched',
    why: 'tasks with a session in the last 24 h',
    empty: 'Nothing touched recently.',
  },
}

/** The home sections in display order (storage.CockpitSections) - the settings screen's list. */
export const SECTION_ORDER: readonly string[] = Object.keys(META)

export function sectionMeta(name: string): SectionMeta {
  return META[name] ?? { title: name, why: '', empty: 'Nothing here.' }
}

/** Rows shown per section before "show all" - the wireframe's five-ish. */
export const SECTION_CAP = 7

export interface CappedSection {
  rows: AttentionRow[]
  /** Rows the API returned but this view hides (expand shows them). */
  collapsed: number
  /** Rows the API itself did not send (its own cap, e.g. the changes digest). */
  elsewhere: number
}

/**
 * Applies the view cap to a section. `expanded` shows every row the API
 * returned; the API's own truncation (total > rows.length) is reported as
 * `elsewhere` and can only be seen on the section's own screen.
 */
export function capSection(section: AttentionSection, expanded: boolean): CappedSection {
  const rows = expanded ? section.rows : section.rows.slice(0, SECTION_CAP)
  return {
    rows,
    collapsed: section.rows.length - rows.length,
    elsewhere: Math.max(0, section.total - section.rows.length),
  }
}

/**
 * A stable key for a row - the keyboard selection lives on it across
 * refetches. Section, project and task id are not enough: the changes
 * section is one row PER EVENT, so two events on one task (or two
 * project-less Slack rows) would share a key and `j` could never step past
 * the first. The event's stamp and title tell them apart and are as stable
 * as the id.
 */
export function rowKey(row: AttentionRow): string {
  return `${row.section}/${row.project}/${row.task_id ?? row.shift ?? ''}/${row.since ?? ''}/${row.title}`
}

/** The bulk dismiss's age: rows older than this many days. */
export const DISMISS_OLDER_DAYS = 14

/** What POST /api/attention/dismiss needs to name one row. */
export function dismissRowOf(row: AttentionRow): DismissRow {
  return {
    section: row.section,
    project: row.project,
    task_id: row.task_id,
    shift: row.shift,
    since: row.since,
  }
}

/**
 * The rows the bulk "dismiss older than N days" takes: dismissable (the API
 * put `dismiss` on them) and of a KNOWN age past the threshold - an unknown
 * age ("since ?") is not old, it is unknown, and stays.
 */
export function olderThan(rows: AttentionRow[], days: number): AttentionRow[] {
  return rows.filter(
    (r) => r.actions.includes('dismiss') && r.age_seconds !== null && r.age_seconds >= days * 86400,
  )
}
