import { describe, expect, it } from 'vitest'

import type { TaskSummary } from '../api/types'
import { groupByStatus, openTaskCount } from './groupByStatus'

function task(id: string, status: string, extra: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id,
    title: id,
    status,
    project: 'p',
    updated: '2026-01-01',
    session_count: 0,
    ...extra,
  }
}

describe('groupByStatus', () => {
  it('follows the project status order and keeps empty columns', () => {
    const groups = groupByStatus([task('a', 'done'), task('b', 'todo')], ['todo', 'doing', 'done'])
    expect(groups.map((g) => g.status)).toEqual(['todo', 'doing', 'done'])
    expect(groups[0].tasks.map((t) => t.id)).toEqual(['b'])
    expect(groups[1].tasks).toEqual([])
    expect(groups[2].tasks.map((t) => t.id)).toEqual(['a'])
  })

  it('never drops a task on a status the project no longer lists', () => {
    const groups = groupByStatus([task('x', 'review')], ['todo'])
    expect(groups.map((g) => g.status)).toEqual(['todo', 'review'])
    expect(groups[1].tasks.map((t) => t.id)).toEqual(['x'])
  })

  it('sorts by order ascending with unset (0) last, then updated descending', () => {
    const groups = groupByStatus(
      [
        task('unset-old', 'todo', { updated: '2026-01-01' }),
        task('o20', 'todo', { order: 20 }),
        task('unset-new', 'todo', { updated: '2026-02-01T10:00:00+02:00' }),
        task('o10', 'todo', { order: 10 }),
      ],
      ['todo'],
    )
    expect(groups[0].tasks.map((t) => t.id)).toEqual(['o10', 'o20', 'unset-new', 'unset-old'])
  })

  it('does not mutate the input', () => {
    const input = [task('b', 'todo', { order: 2 }), task('a', 'todo', { order: 1 })]
    groupByStatus(input, ['todo'])
    expect(input.map((t) => t.id)).toEqual(['b', 'a'])
  })
})

describe('openTaskCount', () => {
  it('sums every status but archived', () => {
    expect(openTaskCount({ todo: 2, doing: 1, done: 4, archived: 9 })).toBe(7)
    expect(openTaskCount({})).toBe(0)
  })
})
