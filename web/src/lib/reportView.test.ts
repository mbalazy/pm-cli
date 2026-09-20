import { describe, expect, it } from 'vitest'

import type { ReportState } from '../api/types'
import { canDo, costLine, paragraphsOf, reportView, totalTokens } from './reportView'

const done: ReportState = {
  enabled: true,
  period: '2026-09-04T18',
  cutoff: '2026-09-04T18:00:00+02:00',
  state: 'done',
  model: 'haiku',
  report: {
    period: '2026-09-04T18',
    cutoff: '2026-09-04T18:00:00+02:00',
    generated: '2026-09-05T08:00:00+02:00',
    model: 'haiku',
    tokens: { input: 100, cache_creation: 0, cache_read: 4000, output: 300 },
    duration_s: 12,
    text: '**Acme**: moved.\n\nDecide.',
    suggestions: [
      {
        id: 'a',
        project: 'acme-api',
        task_id: 'acme-api-1',
        action: 'back_to_todo',
        text: 'lifted',
      },
      { id: 'b', task_id: 'x-9', text: 'unknown' },
    ],
    dismissed: ['b'],
    events: 3,
    rows: 2,
  },
}

describe('reportView', () => {
  it('done: paragraphs, open suggestions minus the dismissed, the cost line', () => {
    const v = reportView(done)
    expect(v.paragraphs).toEqual(['**Acme**: moved.', 'Decide.'])
    expect(v.open.map((s) => s.id)).toEqual(['a'])
    expect(v.dismissedCount).toBe(1)
    expect(v.cost).toBe('haiku · 4.4k tokens · 12 s')
    expect(v.canWrite).toBe(true)
    expect(totalTokens(done.report?.tokens)).toBe(4400)
    expect(costLine({ ...done.report!, tokens: undefined, duration_s: 0 })).toBe('haiku')
  })
  it('off, none, writing, error say what they are', () => {
    expect(reportView({ ...done, state: 'off', enabled: false, report: undefined }).status).toMatch(
      /off/,
    )
    expect(reportView({ ...done, state: 'off', enabled: false, report: undefined }).canWrite).toBe(
      false,
    )
    expect(reportView({ ...done, state: 'none', report: undefined }).status).toMatch(/no report/)
    const w = reportView({ ...done, state: 'writing' })
    expect(w.status).toMatch(/writing/)
    expect(w.canWrite).toBe(false)
    const e = reportView({
      ...done,
      state: 'error',
      report: { ...done.report!, text: undefined, error: 'boom' },
    })
    expect(e.error).toBe('boom')
    expect(e.paragraphs).toEqual([])
    expect(reportView(undefined).status).toBe('loading…')
  })
  it('canDo needs an action and a project; paragraphs split on blank lines', () => {
    expect(canDo({ id: 'a', project: 'p', task_id: 't', action: 'kill', text: '' })).toBe(true)
    expect(canDo({ id: 'a', task_id: 't', action: 'kill', text: '' })).toBe(false)
    expect(canDo({ id: 'a', project: 'p', task_id: 't', text: '' })).toBe(false)
    expect(paragraphsOf('a\n\n\n b \n')).toEqual(['a', 'b'])
  })
})
