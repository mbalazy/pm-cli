import { describe, expect, it } from 'vitest'

import { calendarDaysAgo, parseStamp, relativeTime } from './relativeTime'

// A fixed local "now": 2026-03-10 15:30 local time.
const now = new Date(2026, 2, 10, 15, 30)

describe('parseStamp', () => {
  it('reads a bare date as local midnight', () => {
    const d = parseStamp('2026-03-10')
    expect(d?.getFullYear()).toBe(2026)
    expect(d?.getMonth()).toBe(2)
    expect(d?.getDate()).toBe(10)
    expect(d?.getHours()).toBe(0)
  })
  it('reads RFC3339 and rejects garbage', () => {
    expect(parseStamp('2026-03-10T12:00:00+00:00')?.getTime()).toBe(Date.UTC(2026, 2, 10, 12))
    expect(parseStamp('yesterday')).toBeNull()
    expect(parseStamp('')).toBeNull()
  })
})

describe('calendarDaysAgo', () => {
  it('counts local calendar days, not 24h blocks', () => {
    expect(calendarDaysAgo(new Date(2026, 2, 10, 0, 1), new Date(2026, 2, 10, 23, 59))).toBe(0)
    expect(calendarDaysAgo(new Date(2026, 2, 9, 23, 59), new Date(2026, 2, 10, 0, 1))).toBe(1)
  })
})

describe('relativeTime', () => {
  it('is empty for an unknown stamp and verbatim for an unparsable one', () => {
    expect(relativeTime('', now)).toBe('')
    expect(relativeTime('n/a', now)).toBe('n/a')
  })
  it('says today for a bare date of the same day', () => {
    expect(relativeTime('2026-03-10', now)).toBe('today')
  })
  it('uses minutes and hours within the same day for a full stamp', () => {
    expect(relativeTime(new Date(2026, 2, 10, 15, 29, 50).toISOString(), now)).toBe('just now')
    expect(relativeTime(new Date(2026, 2, 10, 15, 18).toISOString(), now)).toBe('12m ago')
    expect(relativeTime(new Date(2026, 2, 10, 12, 0).toISOString(), now)).toBe('3h ago')
  })
  it('switches to calendar days across midnight', () => {
    expect(relativeTime(new Date(2026, 2, 9, 23, 59).toISOString(), now)).toBe('1d ago')
    expect(relativeTime('2026-03-05', now)).toBe('5d ago')
  })
  it('does not pretend a future stamp is old', () => {
    expect(relativeTime('2026-03-11', now)).toBe('in the future')
  })
})
