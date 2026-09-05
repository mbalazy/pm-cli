import type { AttentionRow, AttentionSection } from '../api/types'

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

/** A stable key for a row (rows without a task id are project rows). */
export function rowKey(row: AttentionRow): string {
  return `${row.section}/${row.project}/${row.task_id ?? ''}`
}
