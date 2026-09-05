import { describe, expect, it } from 'vitest'

import type { AttentionRow, RunRow } from '../api/types'
import {
  filterProjects,
  filterRuns,
  isUnfinished,
  needsMeIndex,
  runGlyph,
  runTimes,
  sortRuns,
} from './runsView'

const run = (project: string, tracker: string, extra: Partial<RunRow> = {}): RunRow => ({
  project,
  tracker,
  title: tracker,
  run: { state: 'done', done: 1, total: 1 },
  acceptance: {},
  ...extra,
})
const nm = (
  project: string,
  task_id: string,
  severity: AttentionRow['severity'],
): AttentionRow => ({
  section: 'needs_me',
  severity,
  project,
  group: project,
  task_id,
  title: task_id,
  reason: 'r',
  age_seconds: 1,
  actions: ['open'],
})

// API order = newest first: b-1, a-2, a-1, a-3(live), vps/x-1
const rows = [
  run('b', 'b-1'),
  run('a', 'a-2'),
  run('a', 'a-1'),
  run('a', 'a-3', {
    run_live: true,
    run_started: '2026-09-05T10:00:00Z',
    run_updated: '2026-09-05T10:20:00Z',
  }),
  run('x', 'x-1', { remote: 'vps' }),
]
// needs_me order (the rank): a-1 crit, b-1 warn
const needsMe = needsMeIndex([nm('a', 'a-1', 'crit'), nm('b', 'b-1', 'warn')])

describe('sortRuns', () => {
  it('newest is the API order untouched', () => {
    expect(sortRuns(rows, 'newest', needsMe)).toBe(rows)
  })
  it('needs_me puts the queue rows first in the QUEUE order, the rest in API order', () => {
    expect(sortRuns(rows, 'needs_me', needsMe).map((r) => r.tracker)).toEqual([
      'a-1',
      'b-1',
      'a-2',
      'a-3',
      'x-1',
    ])
  })
  it('project groups by project, keeping the API order inside', () => {
    expect(sortRuns(rows, 'project', needsMe).map((r) => r.tracker)).toEqual([
      'a-2',
      'a-1',
      'a-3',
      'b-1',
      'x-1',
    ])
  })
})

describe('filters and glyphs', () => {
  it('unfinished = live or in the queue; remote rows always kept', () => {
    expect(isUnfinished(rows[0], needsMe)).toBe(true) // b-1 in queue
    expect(isUnfinished(rows[1], needsMe)).toBe(false) // a-2 finished, not in queue
    expect(isUnfinished(rows[3], needsMe)).toBe(true) // live
    expect(isUnfinished(rows[4], needsMe)).toBe(true) // remote
    expect(filterRuns(rows, true, needsMe).map((r) => r.tracker)).toEqual([
      'b-1',
      'a-1',
      'a-3',
      'x-1',
    ])
    expect(filterRuns(rows, false, needsMe)).toBe(rows)
  })
  it('filterProjects keeps the members only', () => {
    expect(filterProjects(rows, ['a']).map((r) => r.tracker)).toEqual(['a-2', 'a-1', 'a-3'])
  })
  it('glyph is the queue severity, ▶ when live, ○ otherwise', () => {
    expect(runGlyph(rows[2], needsMe)).toBe('✗')
    expect(runGlyph(rows[0], needsMe)).toBe('▲')
    expect(runGlyph(rows[3], needsMe)).toBe('▶')
    expect(runGlyph(rows[1], needsMe)).toBe('○')
    expect(runGlyph(run('r', '', { remote: 'vps', note: 'unreachable' }), needsMe)).toBe('?')
  })
})

describe('runTimes', () => {
  const now = new Date('2026-09-05T12:00:00Z')
  it('a finished run: duration and time since the end, no heartbeat', () => {
    const r = run('a', 'a-1', {
      run_started: '2026-09-05T09:00:00Z',
      run_updated: '2026-09-05T10:30:00Z',
    })
    expect(runTimes(r, now)).toEqual({ duration: '1h', sinceEnd: '1h', heartbeat: '' })
  })
  it('a live run: duration up to now and the heartbeat age, no end', () => {
    expect(runTimes(rows[3], now)).toEqual({ duration: '2h', sinceEnd: '', heartbeat: '1h' })
  })
  it('no run = three empties', () => {
    expect(runTimes(rows[0], now)).toEqual({ duration: '', sinceEnd: '', heartbeat: '' })
  })
})
