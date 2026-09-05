import { describe, expect, it } from 'vitest'

import { refreshTimes } from './refreshTimes'

const src = (name: string, last_fetch?: string) => ({ name, enabled: true, events: 0, last_fetch })

describe('refreshTimes', () => {
  it('takes the newest fetch and adds the interval', () => {
    const t1 = new Date(2026, 8, 5, 13, 30).toISOString()
    const t0 = new Date(2026, 8, 5, 13, 0).toISOString()
    expect(refreshTimes([src('pm', t0), src('git', t1), src('gh')], 1800)).toEqual({
      refreshed: '13:30',
      next: '14:00',
    })
  })
  it('a next outside the window moves to the window start (today or tomorrow)', () => {
    const late = new Date(2026, 8, 5, 21, 37).toISOString()
    expect(refreshTimes([src('git', late)], 1800, '07:00-20:00')).toEqual({
      refreshed: '21:37',
      next: '07:00',
    })
    const early = new Date(2026, 8, 5, 5, 50).toISOString()
    expect(refreshTimes([src('git', early)], 1800, '07:00-20:00').next).toBe('07:00')
    const inside = new Date(2026, 8, 5, 13, 30).toISOString()
    expect(refreshTimes([src('git', inside)], 1800, '07:00-20:00').next).toBe('14:00')
    // the last slot of the window: 19:45 + 30 min = 20:15 is outside
    const edge = new Date(2026, 8, 5, 19, 45).toISOString()
    expect(refreshTimes([src('git', edge)], 1800, '07:00-20:00').next).toBe('07:00')
    // an unparsable window changes nothing
    expect(refreshTimes([src('git', late)], 1800, 'always').next).toBe('22:07')
  })
  it('no fetch yet = empty; no interval = no next', () => {
    expect(refreshTimes([src('pm')], 1800)).toEqual({ refreshed: '', next: '' })
    expect(refreshTimes(undefined, 1800)).toEqual({ refreshed: '', next: '' })
    const t = new Date(2026, 8, 5, 13, 30).toISOString()
    expect(refreshTimes([src('pm', t)], 0)).toEqual({ refreshed: '13:30', next: '' })
    expect(refreshTimes([src('pm', t)], undefined).next).toBe('')
  })
})
