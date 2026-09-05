import type { AttentionRow, RunRow } from '../api/types'
import { ageLabel } from './ageLabel'
import { severityGlyph } from './glyphs'

// The Runs screen's order, filter, glyph and the three times. The RANKING is
// the API's: "needs me first" is a join with the needs_me section of
// /api/attention (its order IS the rank, failed › visual › landed without
// acceptance › partial › live claim), never a rule of this file. What this
// file owns is the join, the stable sorts, and turning raw stamps into text.

export type RunSort = 'needs_me' | 'newest' | 'project'
export const RUN_SORTS: readonly RunSort[] = ['needs_me', 'newest', 'project']

/** The join key of a local run row and a needs_me row. */
export function runKey(row: Pick<RunRow, 'remote' | 'project' | 'tracker'>): string {
  return `${row.remote ?? ''}/${row.project}/${row.tracker ?? ''}`
}

/** needs_me rows by run key, in the API's order (the index is the rank). */
export function needsMeIndex(rows: AttentionRow[] | undefined): Map<string, AttentionRow> {
  const m = new Map<string, AttentionRow>()
  for (const r of rows ?? []) {
    if (r.task_id) m.set(`/${r.project}/${r.task_id}`, r)
  }
  return m
}

function rank(row: RunRow, needsMe: Map<string, AttentionRow>): number {
  const r = needsMe.get(runKey(row))
  if (!r) return Number.POSITIVE_INFINITY
  let i = 0
  for (const k of needsMe.keys()) {
    if (k === runKey(row)) return i
    i++
  }
  return Number.POSITIVE_INFINITY
}

/**
 * Orders the rows. `newest` is the API's own order (SortRunRows: most recent
 * activity first) and is returned as is; the other two are STABLE sorts over
 * it, so ties keep the API's order.
 */
export function sortRuns(
  rows: RunRow[],
  sort: RunSort,
  needsMe: Map<string, AttentionRow>,
): RunRow[] {
  if (sort === 'newest') return rows
  const indexed = rows.map((row, i) => ({ row, i }))
  if (sort === 'project') {
    indexed.sort((a, b) => {
      const pa = `${a.row.remote ?? ''}/${a.row.project}`
      const pb = `${b.row.remote ?? ''}/${b.row.project}`
      if (pa !== pb) return pa < pb ? -1 : 1
      return a.i - b.i
    })
  } else {
    indexed.sort((a, b) => {
      const ra = rank(a.row, needsMe)
      const rb = rank(b.row, needsMe)
      if (ra !== rb) return ra - rb
      return a.i - b.i
    })
  }
  return indexed.map((x) => x.row)
}

/**
 * "Unfinished" = the run is alive, or the queue says the human is needed
 * (the needs_me row exists). A REMOTE row is always kept: it was fetched on
 * purpose and the local queue knows nothing about it. Placeholder rows
 * (a note, no tracker) are kept too - a sleeping VPS is information.
 */
export function isUnfinished(row: RunRow, needsMe: Map<string, AttentionRow>): boolean {
  if (row.remote || row.note) return true
  return Boolean(row.run_live) || needsMe.has(runKey(row))
}

export function filterRuns(
  rows: RunRow[],
  unfinishedOnly: boolean,
  needsMe: Map<string, AttentionRow>,
): RunRow[] {
  return unfinishedOnly ? rows.filter((r) => isUnfinished(r, needsMe)) : rows
}

/** Only the rows of the given projects (a group's members). */
export function filterProjects(rows: RunRow[], projects: string[]): RunRow[] {
  const set = new Set(projects)
  return rows.filter((r) => set.has(r.project))
}

/** The row's glyph: the queue's severity where the queue has it, ▶ for a live run, ○ otherwise. */
export function runGlyph(row: RunRow, needsMe: Map<string, AttentionRow>): string {
  const r = needsMe.get(runKey(row))
  if (r) return severityGlyph(r.severity)
  if (row.run_live) return '▶'
  if (row.note) return '?'
  return '○'
}

export interface RunTimes {
  /** How long the run ran (or has been running). */
  duration: string
  /** How long since it ended - empty while live. */
  sinceEnd: string
  /** How long since the last heartbeat - only while live. */
  heartbeat: string
}

function seconds(from: string | undefined, to: Date): number | null {
  if (!from) return null
  const t = new Date(from).getTime()
  if (Number.isNaN(t)) return null
  return Math.floor((to.getTime() - t) / 1000)
}

/** The three times off the raw stamps; empty where the row has no run. */
export function runTimes(row: RunRow, now: Date = new Date()): RunTimes {
  const none = { duration: '', sinceEnd: '', heartbeat: '' }
  if (!row.run_started) return none
  const end = row.run_live ? now : new Date(row.run_updated ?? row.run_started)
  const dur = seconds(row.run_started, end)
  const since = seconds(row.run_updated, now)
  return {
    duration: dur === null ? '' : ageLabel(Math.max(0, dur)),
    sinceEnd: row.run_live || since === null ? '' : ageLabel(since),
    heartbeat: row.run_live && since !== null ? ageLabel(since) : '',
  }
}
