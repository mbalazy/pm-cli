import type { AcceptCell, RunCell } from '../api/types'

// The RUN / ACCEPTANCE cells rendered exactly as `pm runs` prints them
// (storage.RunCell.String / AcceptCell.String) - one vocabulary across the
// board, the CLI and this page.

export function runCellText(c: RunCell): string {
  if (!c.state) return '-'
  if (c.state === 'prepped' || c.total === 0) return c.state
  return `${c.state} ${c.done}/${c.total}`
}

export function acceptCellText(c: AcceptCell): string {
  if (!c.state) return '-'
  let s = c.state
  if (c.host && c.age) s += ` (${c.host}, ${c.age})`
  else if (c.host) s += ` (${c.host})`
  else if (c.age) s += ` (${c.age})`
  if (c.visual_claims_open && c.visual_claims_open > 0) {
    s += `, ${c.visual_claims_open} visual claim(s) open`
  }
  return s
}
