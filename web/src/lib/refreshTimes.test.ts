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
  it('no fetch yet = empty; no interval = no next', () => {
    expect(refreshTimes([src('pm')], 1800)).toEqual({ refreshed: '', next: '' })
    expect(refreshTimes(undefined, 1800)).toEqual({ refreshed: '', next: '' })
    const t = new Date(2026, 8, 5, 13, 30).toISOString()
    expect(refreshTimes([src('pm', t)], 0)).toEqual({ refreshed: '13:30', next: '' })
    expect(refreshTimes([src('pm', t)], undefined).next).toBe('')
  })
})
