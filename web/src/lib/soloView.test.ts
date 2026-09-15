import { describe, expect, it } from 'vitest'

import type { Shift } from '../api/types'
import { soloRows } from './soloView'

const shift = (over: Partial<Shift>): Shift => ({
  project: 'a',
  id: 'x',
  kind: 'solo',
  date: '2026-09-10',
  open: false,
  status_line: '',
  tasks: [],
  file: '/f.md',
  ...over,
})

const counts = { done: 0, partial: 0, not_done: 0, parked: 0, untouched: 0 }

describe('soloRows', () => {
  it('words the glyph, the state, the name and the outcome, narrowed to the projects', () => {
    const rows = soloRows(
      [
        shift({ id: 'o', open: true, tasks: [{ id: 'a-1', title: 't', status: 'todo' }] }),
        shift({
          id: 'r',
          report: '/r.md',
          closed: new Date(Date.now() - 2 * 3600_000).toISOString(),
          tasks: [
            { id: 'a-2', title: 't', status: 'todo', outcome: 'done' },
            { id: 'a-3', title: 't' },
          ],
          summary: { ...counts, title: 'ACME-1, ekrany', done: 1, partial: 1 },
        }),
        shift({ id: 'n', summary: { ...counts, done: 2 } }),
        shift({ id: 'b', project: 'b' }),
      ],
      ['a'],
    )
    expect(rows.map((r) => r.glyph)).toEqual(['▶', '✎', '○'])
    expect(rows[0].when).toBe('open')
    expect(rows[1].when).toMatch(/^closed /)
    expect(rows[2].when).toBe('closed')
    expect(rows.map((r) => r.title)).toEqual(['a-1', 'ACME-1, ekrany', ''])
    expect(rows.map((r) => r.outcome)).toEqual(['', '1 done · 1 partial', '2 done'])
    expect(rows.map((r) => r.tone)).toEqual(['', 'warn', 'ok'])
    expect(soloRows(undefined)).toEqual([])
  })
})
