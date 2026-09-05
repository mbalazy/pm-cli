import { describe, expect, it } from 'vitest'

import type { ChangeEvent, Project, RunRow, Tracker } from '../api/types'
import {
  activeRepo,
  filterChanges,
  groupMembers,
  groupName,
  groupOf,
  parseTab,
  stepTab,
  trackerRows,
} from './groupView'

const p = (slug: string, group?: string, group_name?: string, archived?: boolean) =>
  ({ slug, name: slug.toUpperCase(), group, group_name, archived }) as Project
const projects = [
  p('acme-api', 'acme', 'ACME'),
  p('acme-zap', 'acme', 'ACME'),
  p('old', 'acme', 'ACME', true),
  p('solo'),
]

describe('tabs', () => {
  it('parses a known tab and defaults to overview', () => {
    expect(parseTab('runs')).toBe('runs')
    expect(parseTab('nope')).toBe('overview')
    expect(parseTab(undefined)).toBe('overview')
  })
  it('steps with wrap-around', () => {
    expect(stepTab('overview', 1)).toBe('board')
    expect(stepTab('changes', 1)).toBe('overview')
    expect(stepTab('overview', -1)).toBe('changes')
  })
})

describe('groups', () => {
  it('finds the group of a repo and the members of a group (archived excluded)', () => {
    expect(groupOf(projects, 'acme-zap')).toBe('acme')
    expect(groupOf(projects, 'solo')).toBe('solo')
    expect(groupOf(projects, 'unknown')).toBe('unknown')
    expect(groupMembers(projects, 'acme').map((m) => m.slug)).toEqual(['acme-api', 'acme-zap'])
    expect(groupMembers(projects, 'solo').map((m) => m.slug)).toEqual(['solo'])
    expect(groupMembers(undefined, 'acme')).toEqual([])
  })
  it('names the group and picks the board repo', () => {
    const acme = groupMembers(projects, 'acme')
    expect(groupName(acme, 'acme')).toBe('ACME')
    expect(groupName([], 'ghost')).toBe('ghost')
    expect(groupName(groupMembers(projects, 'solo'), 'solo')).toBe('SOLO')
    expect(activeRepo(acme, 'acme-zap')).toBe('acme-zap')
    expect(activeRepo(acme, 'other')).toBe('acme-api')
    expect(activeRepo([], undefined)).toBe('')
  })
})

describe('joins', () => {
  it('trackerRows keeps member order and joins the run row by project + tracker', () => {
    const tr = (id: string): Tracker => ({ id, title: id, status: 'doing', total: 1, progress: {} })
    const runs: RunRow[] = [
      {
        project: 'acme-zap',
        tracker: 'z-1',
        run: { state: 'done', done: 1, total: 1 },
        acceptance: {},
      },
      {
        project: 'acme-api',
        tracker: 'l-1',
        remote: 'vps',
        run: { state: 'done', done: 1, total: 1 },
        acceptance: {},
      },
    ]
    const rows = trackerRows(['acme-api', 'acme-zap'], [[tr('l-1')], [tr('z-1'), tr('z-2')]], runs)
    expect(rows.map((r) => `${r.project}/${r.tracker.id}/${r.run?.run.state ?? '-'}`)).toEqual([
      'acme-api/l-1/-',
      'acme-zap/z-1/done',
      'acme-zap/z-2/-',
    ])
  })
  it('filterChanges keeps one group', () => {
    const ev = (group: string) => ({ id: group, group }) as ChangeEvent
    expect(filterChanges([ev('a'), ev('b'), ev('a')], 'a')).toHaveLength(2)
    expect(filterChanges(undefined, 'a')).toEqual([])
  })
})
