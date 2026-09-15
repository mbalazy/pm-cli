import type { Shift } from '../api/types'
import { relativeTime } from './relativeTime'
import { countsText, unfinished } from './soloReportView'

// The Runs screen's solo table (pm-cli-136): one line per /solo shift, open
// ones first (the API's order), narrowed to a group's projects when asked.

export interface SoloRow {
  shift: Shift
  /** ▶ open, ✎ closed with a report, ○ closed without one. */
  glyph: string
  /** "open" or "closed <relative time>". */
  when: string
  /** The report's name for the shift, else the queued task ids. */
  title: string
  /** "6 done · 1 untouched"; '' until a report or the Progress says anything. */
  outcome: string
  /** warn when a task came back unfinished, ok when something got done. */
  tone: string
}

export function soloRows(shifts: Shift[] | undefined, projects?: string[]): SoloRow[] {
  const keep = projects ? new Set(projects) : undefined
  return (shifts ?? [])
    .filter((s) => !keep || keep.has(s.project))
    .map((s) => ({
      shift: s,
      glyph: s.open ? '▶' : s.report ? '✎' : '○',
      when: s.open ? 'open' : s.closed ? `closed ${relativeTime(s.closed)}` : 'closed',
      title: s.summary?.title || s.tasks.map((t) => t.id).join(', '),
      outcome: countsText(s.summary),
      tone: unfinished(s.summary) ? 'warn' : s.summary?.done ? 'ok' : '',
    }))
}
