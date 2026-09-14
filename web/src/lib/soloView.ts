import type { Shift } from '../api/types'
import { relativeTime } from './relativeTime'

// The Runs screen's solo table (pm-cli-136): one line per /solo shift, open
// ones first (the API's order), narrowed to a group's projects when asked.

export interface SoloRow {
  shift: Shift
  /** ▶ open, ✎ closed with a report, ○ closed without one. */
  glyph: string
  /** "open" or "closed <relative time>". */
  when: string
  /** "<id> <status>" per queued task. */
  tasks: string
}

export function soloRows(shifts: Shift[] | undefined, projects?: string[]): SoloRow[] {
  const keep = projects ? new Set(projects) : undefined
  return (shifts ?? [])
    .filter((s) => !keep || keep.has(s.project))
    .map((s) => ({
      shift: s,
      glyph: s.open ? '▶' : s.report ? '✎' : '○',
      when: s.open ? 'open' : s.closed ? `closed ${relativeTime(s.closed)}` : 'closed',
      tasks: s.tasks.map((t) => (t.status ? `${t.id} ${t.status}` : t.id)).join(', '),
    }))
}
