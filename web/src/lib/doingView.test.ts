import { describe, expect, it } from 'vitest'

import type { TaskSummary } from '../api/types'
import {
  activityAgeSeconds,
  activityOf,
  doingByActivity,
  isIdle,
  progressText,
  trackerIndex,
} from './doingView'

const task = (id: string, updated: string, status_changed?: string, status = 'doing') =>
  ({
    id,
    title: id,
    status,
    project: 'p',
    updated,
    status_changed,
    session_count: 0,
  }) as TaskSummary

const now = new Date(2026, 8, 10, 12, 0)

describe('activityOf / isIdle', () => {
  it('takes the later of the two stamps and tolerates a missing one', () => {
    expect(activityOf(task('a', '2026-09-01', '2026-09-05T10:00:00Z'))?.toISOString()).toBe(
      '2026-09-05T10:00:00.000Z',
    )
    expect(activityOf(task('a', '2026-09-08', '2026-09-05T10:00:00Z'))?.getDate()).toBe(8)
    expect(activityOf(task('a', '2026-09-08'))?.getDate()).toBe(8)
    expect(activityOf(task('a', 'garbage'))).toBeNull()
  })
  it('idle past the threshold, unknown counts as idle', () => {
    expect(isIdle(task('a', '2026-09-09'), 7, now)).toBe(false)
    expect(isIdle(task('a', '2026-09-01'), 7, now)).toBe(true)
    expect(isIdle(task('a', 'garbage'), 7, now)).toBe(true)
    expect(activityAgeSeconds(task('a', 'garbage'), now)).toBeNull()
    expect(activityAgeSeconds(task('a', '2026-09-10'), now)).toBe(12 * 3600)
  })
})

describe('doingByActivity', () => {
  it('merges the repos, keeps doing only, freshest first, unknown last', () => {
    const a = [task('a-1', '2026-09-01'), task('a-2', '2026-09-09', undefined, 'todo')]
    const b = [task('b-1', '2026-09-05', '2026-09-09T08:00:00Z'), task('b-2', 'garbage')]
    expect(doingByActivity([a, b]).map((t) => t.id)).toEqual(['b-1', 'a-1', 'b-2'])
  })
})

describe('trackers', () => {
  it('indexes rollups across repos and words the progress map', () => {
    const idx = trackerIndex([
      [{ id: 'a-9', title: 't', status: 'doing', total: 4, progress: { merged: 3, todo: 1 } }],
      undefined,
    ])
    expect(idx.get('a-9')?.total).toBe(4)
    expect(progressText(idx.get('a-9')!)).toBe('3 merged · 1 todo')
    expect(progressText({ total: 2, progress: {} })).toBe('0/2')
  })
})
