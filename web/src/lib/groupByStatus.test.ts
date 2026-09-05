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

  it('sorts like storage.LessByOrder: order ascending with unset (0) FIRST, then id number, then updated descending', () => {
    const groups = groupByStatus(
      [
        task('p-7', 'todo', { updated: '2026-01-01' }),
        task('p-20', 'todo', { order: 20 }),
        task('p-3', 'todo', { updated: '2026-02-01T10:00:00+02:00' }),
        task('p-10', 'todo', { order: 10 }),
      ],
      ['todo'],
    )
    // 0-order tasks first (the TUI puts a freshly added task at the top), ties on order broken by the id number
    expect(groups[0].tasks.map((t) => t.id)).toEqual(['p-3', 'p-7', 'p-10', 'p-20'])
  })

  it('falls back to updated descending when order and id number tie', () => {
    const groups = groupByStatus(
      [
        task('old', 'todo', { updated: '2026-01-01' }),
        task('new', 'todo', { updated: '2026-02-01T10:00:00+02:00' }),
      ],
      ['todo'],
    )
    expect(groups[0].tasks.map((t) => t.id)).toEqual(['new', 'old'])
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
