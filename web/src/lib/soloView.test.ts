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

describe('soloRows', () => {
  it('words the glyph, the state and the queue, narrowed to the projects', () => {
    const rows = soloRows(
      [
        shift({ id: 'o', open: true, tasks: [{ id: 'a-1', title: 't', status: 'todo' }] }),
        shift({
          id: 'r',
          report: '/r.md',
          closed: new Date(Date.now() - 2 * 3600_000).toISOString(),
          tasks: [
            { id: 'a-2', title: 't', status: 'done' },
            { id: 'a-3', title: 't' },
          ],
        }),
        shift({ id: 'n' }),
        shift({ id: 'b', project: 'b' }),
      ],
      ['a'],
    )
    expect(rows.map((r) => r.glyph)).toEqual(['▶', '✎', '○'])
    expect(rows[0].when).toBe('open')
    expect(rows[1].when).toMatch(/^closed /)
    expect(rows[1].tasks).toBe('a-2 done, a-3')
    expect(rows[2].when).toBe('closed')
    expect(soloRows(undefined)).toEqual([])
  })
})
