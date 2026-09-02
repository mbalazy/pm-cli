import { describe, expect, it } from 'vitest'

import type { Project, TaskSummary } from '../api/types'
import { paletteItems, projectPath, taskPath } from './paletteItems'

const projects: Project[] = [
  { slug: 'alpha', name: 'Alpha App', task_counts: {}, statuses: [], landing_statuses: [] },
  { slug: 'old', name: 'Old', task_counts: {}, statuses: [], landing_statuses: [], archived: true },
]
const tasks: TaskSummary[] = [
  {
    id: 'alpha-1',
    title: 'Fix login',
    status: 'todo',
    project: 'alpha',
    updated: '',
    session_count: 0,
  },
  {
    id: 'alpha-2',
    title: 'Ship it',
    status: 'todo',
    project: 'alpha',
    updated: '',
    session_count: 0,
  },
]
const recent = [{ project: 'alpha', id: 'alpha-2', title: 'Ship it' }]

describe('paletteItems', () => {
  it('lists projects, runs and recent tasks on an empty query, never archived projects', () => {
    const items = paletteItems({ projects, tasks, recent, query: '' })
    expect(items.map((i) => i.id)).toEqual(['alpha', 'runs', 'alpha-2'])
    expect(items[0].to).toBe('/p/alpha')
    expect(items[2].to).toBe('/p/alpha/t/alpha-2')
  })
  it('filters case-insensitively by id and title, projects by name and slug', () => {
    expect(paletteItems({ projects, tasks, recent, query: 'LOGIN' }).map((i) => i.id)).toEqual([
      'alpha-1',
    ])
    expect(paletteItems({ projects, tasks, recent, query: 'alpha-2' }).map((i) => i.id)).toEqual([
      'alpha-2',
    ])
    expect(paletteItems({ projects, tasks, recent, query: 'app' }).map((i) => i.id)).toEqual([
      'alpha',
    ])
    expect(paletteItems({ projects, tasks, recent, query: 'run' }).map((i) => i.id)).toEqual([
      'runs',
    ])
  })
  it('searches the loaded tasks, not the recent list, once a query is typed', () => {
    expect(paletteItems({ projects, tasks: [], recent, query: 'ship' })).toEqual([])
  })
})

describe('paths', () => {
  it('encodes path segments', () => {
    expect(projectPath('a b')).toBe('/p/a%20b')
    expect(taskPath('p', 'x/y')).toBe('/p/p/t/x%2Fy')
  })
})
