import type { AttentionRow } from '../api/types'

// Every state is a GLYPH plus text, never a colour alone (the user's rule:
// the glyph has to survive a monochrome screen). One table for the severity
// glyphs, one for the sidebar's counter columns.

export type Severity = AttentionRow['severity']

export const SEVERITY_GLYPH: Record<Severity, string> = {
  crit: '✗',
  warn: '▲',
  info: '●',
  ok: '○',
}

export function severityGlyph(severity: string): string {
  return SEVERITY_GLYPH[severity as Severity] ?? '·'
}

/**
 * A row's glyph: the section's own symbol where it has one (a waiting row is
 * an hourglass whatever its severity, a quiet project a clock), the
 * severity's otherwise.
 */
export function rowGlyph(row: Pick<AttentionRow, 'section' | 'severity'>): string {
  switch (row.section) {
    case 'waiting':
      return '⧗'
    case 'stuck_projects':
      return '◔'
    case 'focus':
      return '●'
    default:
      return severityGlyph(row.severity)
  }
}

/** The sidebar's four counter columns, in order: failed, visual, waiting, quiet. */
export const COUNTER_COLUMNS = [
  { key: 'failed', glyph: '✗', title: 'failed or crashed runs' },
  { key: 'visual', glyph: '👁', title: 'open visual claims' },
  { key: 'waiting', glyph: '⧗', title: 'tasks waiting on someone' },
  { key: 'quiet', glyph: '◔', title: 'quiet: stuck or idle' },
] as const

export type CounterKey = (typeof COUNTER_COLUMNS)[number]['key']
