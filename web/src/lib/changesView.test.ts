import { describe, expect, it } from 'vitest'

import type { ChangeEvent } from '../api/types'
import {
  cutoffText,
  eventGroups,
  eventTimeText,
  filterEvents,
  sourceChips,
  sourceCounts,
  toggleSource,
} from './changesView'

const ev = (id: string, source: string, group: string): ChangeEvent => ({
  id,
  ts: '2026-01-02T09:00:00Z',
  source,
  project: group,
  group,
  title: id,
  severity: 'info',
  seen: false,
})
const events = [
  ev('a', 'pm', 'acme'),
  ev('b', 'git', 'alpha'),
  ev('c', 'pm', 'alpha'),
  ev('d', 'github', 'acme'),
]

describe('filterEvents', () => {
  it('an empty source set keeps every source; a group narrows; both compose', () => {
    expect(filterEvents(events, { sources: new Set(), group: '' })).toHaveLength(4)
    expect(filterEvents(events, { sources: new Set(['pm']), group: '' }).map((e) => e.id)).toEqual([
      'a',
      'c',
    ])
    expect(filterEvents(events, { sources: new Set(), group: 'acme' }).map((e) => e.id)).toEqual([
      'a',
      'd',
    ])
    expect(
      filterEvents(events, { sources: new Set(['pm']), group: 'acme' }).map((e) => e.id),
    ).toEqual(['a'])
  })
  it('counts per source and lists groups in first-seen order', () => {
    expect([...sourceCounts(events)]).toEqual([
      ['pm', 2],
      ['git', 1],
      ['github', 1],
    ])
    expect(eventGroups(events)).toEqual(['acme', 'alpha'])
  })
  it('toggling a source off from "all" keeps the rest; toggling the last one back on means all again', () => {
    const all = ['pm', 'git', 'github']
    const s1 = toggleSource(new Set(), 'git', all)
    expect([...s1].sort()).toEqual(['github', 'pm'])
    expect(toggleSource(s1, 'git', all).size).toBe(0)
  })
})

describe('time and chips', () => {
  const now = new Date(2026, 0, 2, 13, 30)
  it('today is HH:MM, yesterday is prefixed, older carries the date', () => {
    expect(eventTimeText(new Date(2026, 0, 2, 9, 5).toISOString(), now)).toBe('09:05')
    expect(eventTimeText(new Date(2026, 0, 1, 21, 40).toISOString(), now)).toBe('yest. 21:40')
    expect(eventTimeText(new Date(2025, 11, 30, 8, 0).toISOString(), now)).toMatch(/Dec 30 08:00/)
    expect(eventTimeText('garbage', now)).toBe('garbage')
  })
  it('a chip per source: off, never, error, ok with the last fetch', () => {
    const chips = sourceChips(
      [
        {
          name: 'pm',
          enabled: true,
          events: 3,
          last_fetch: new Date(2026, 0, 2, 12, 0).toISOString(),
        },
        { name: 'git', enabled: true, events: 0, error: 'gh: not logged in' },
        { name: 'github', enabled: true, events: 0 },
        { name: 'slack', enabled: false, events: 0 },
      ],
      now,
    )
    expect(chips.map((c) => [c.name, c.state, c.text])).toEqual([
      ['pm', 'ok', 'last 12:00'],
      ['git', 'error', 'error: gh: not logged in'],
      ['github', 'never', 'never fetched'],
      ['slack', 'off', 'off'],
    ])
    expect(sourceChips(undefined)).toEqual([])
  })
  it('the cutoff reads as yesterday/today at the hour', () => {
    expect(cutoffText(new Date(2026, 0, 1, 18, 0).toISOString(), now)).toBe('since yesterday 18:00')
    expect(cutoffText(new Date(2026, 0, 2, 6, 0).toISOString(), now)).toBe('since today 06:00')
    expect(cutoffText('', now)).toBe('')
  })
})
