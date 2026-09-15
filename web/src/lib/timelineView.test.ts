import { describe, expect, it } from 'vitest'

import type { Project, TimelineEntry, TimelineRead } from '../api/types'
import { dateOf, entriesHeading, leftOffView, lineOf, staleLine } from './timelineView'

const project = (notes?: string) =>
  ({
    slug: 'p',
    name: 'P',
    group: 'p',
    group_name: 'P',
    task_counts: {},
    statuses: [],
    landing_statuses: [],
    notes,
  }) as Project

// Local 10:00 of a September day, as the server stamps it.
const at = (day: number) => new Date(2026, 8, day, 10, 0).toISOString()

const entry = (id: string, day: number, kind: string, text: string, refs?: string[]) =>
  ({ id, ts: at(day), kind, text, refs }) as TimelineEntry

const read = (over: Partial<TimelineRead>): TimelineRead => ({
  project: 'p',
  state: null,
  since: [],
  stale: false,
  entries_since: 0,
  days_since: 0,
  total: 0,
  ...over,
})

describe('leftOffView', () => {
  it('falls back to the notes with no read and with an empty timeline', () => {
    expect(leftOffView(project('old notes'), undefined)).toEqual({
      kind: 'notes',
      notes: 'old notes',
    })
    expect(leftOffView(project('old notes'), read({ note: 'no timeline entries yet' }))).toEqual({
      kind: 'notes',
      notes: 'old notes',
    })
    expect(leftOffView(project(), undefined)).toEqual({ kind: 'notes', notes: '' })
  })

  it('shows the state and the entries after it, oldest first, instead of the notes', () => {
    const v = leftOffView(
      project('old notes'),
      read({
        state: entry('s', 15, 'state', 'where we stand'),
        since: [entry('a', 16, 'decision', 'D'), entry('b', 17, 'event', 'E', ['p-3'])],
        entries_since: 2,
        days_since: 2,
        total: 3,
      }),
    )
    expect(v).toEqual({
      kind: 'timeline',
      state: { id: 's', date: '2026-09-15', kind: 'state', text: 'where we stand', refs: [] },
      entries: [
        { id: 'a', date: '2026-09-16', kind: 'decision', text: 'D', refs: [] },
        { id: 'b', date: '2026-09-17', kind: 'event', text: 'E', refs: [{ label: 'p-3' }] },
      ],
      heading: '2 entries since this state',
      stale: '',
    })
  })

  it('an events-only timeline has no state and says which entries it shows', () => {
    const v = leftOffView(
      project('old notes'),
      read({ since: [entry('a', 1, 'event', 'A'), entry('b', 2, 'event', 'B')], total: 12 }),
    )
    expect(v.kind === 'timeline' && v.state).toBeNull()
    expect(v.kind === 'timeline' && v.heading).toBe('no state yet · the newest 2 of 12 entries')
  })
})

describe('staleLine', () => {
  it('is empty while the state is fresh', () => {
    expect(staleLine(read({ entries_since: 9, days_since: 6 }))).toBe('')
  })

  it('words the age and the entries after the state', () => {
    expect(staleLine(read({ stale: true, entries_since: 12, days_since: 3 }))).toBe(
      'this state is 3 days old with 12 entries after it - time to write a new one',
    )
    expect(staleLine(read({ stale: true, entries_since: 1, days_since: 1 }))).toBe(
      'this state is 1 day old with 1 entry after it - time to write a new one',
    )
  })
})

describe('entriesHeading / lineOf / dateOf', () => {
  it('says when nothing happened after the state', () => {
    const state = entry('s', 15, 'state', 'S')
    expect(entriesHeading(read({ state, total: 1 }))).toBe('nothing since this state')
    expect(entriesHeading(read({ state, since: [entry('a', 16, 'event', 'A')], total: 2 }))).toBe(
      '1 entry since this state',
    )
  })

  it('links the refs that are URLs and keeps the rest as text', () => {
    expect(
      lineOf(entry('a', 16, 'event', 'A', ['https://x.test/pr/1', 'docs/plan.md'])).refs,
    ).toEqual([
      { label: 'https://x.test/pr/1', href: 'https://x.test/pr/1' },
      { label: 'docs/plan.md' },
    ])
  })

  it('reads a bare date and shows an unparsable stamp as it is', () => {
    expect(dateOf('2026-09-15')).toBe('2026-09-15')
    expect(dateOf('garbage')).toBe('garbage')
  })
})
