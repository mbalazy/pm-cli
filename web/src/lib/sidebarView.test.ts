import { describe, expect, it } from 'vitest'

import type { GroupSummary, Project } from '../api/types'
import { asleepProjects, groupTarget, sidebarLayout, sortGroups } from './sidebarView'

const g = (slug: string, last_activity?: string): GroupSummary => ({
  slug,
  name: slug,
  projects: [slug],
  worst: 'ok',
  failed: 0,
  visual: 0,
  waiting: 0,
  quiet: 0,
  last_activity,
})

describe('sidebarLayout', () => {
  it('defaults to columns when the config is missing', () => {
    expect(sidebarLayout(undefined)).toEqual({
      variant: 'columns',
      showCounts: true,
      showNames: true,
      showRepos: true,
      width: undefined,
    })
  })
  it('plain drops the counters, rail drops names and repos too', () => {
    const plain = sidebarLayout({ variant: 'plain', show_repos: true, sort: 'worst', width: 0 })
    expect(plain.showCounts).toBe(false)
    expect(plain.showNames).toBe(true)
    expect(plain.showRepos).toBe(true)
    const rail = sidebarLayout({ variant: 'rail', show_repos: true, sort: 'worst', width: 240 })
    expect(rail).toMatchObject({ showCounts: false, showNames: false, showRepos: false })
    expect(rail.width).toBe('240px')
  })
  it('an unknown variant is columns, show_repos off is honoured', () => {
    const l = sidebarLayout({ variant: 'bogus', show_repos: false, sort: 'worst', width: 0 })
    expect(l.variant).toBe('columns')
    expect(l.showRepos).toBe(false)
  })
})

describe('sortGroups', () => {
  const groups = [g('a', '2026-09-01T00:00:00Z'), g('b'), g('c', '2026-09-05T00:00:00Z')]
  it('keeps the API order for worst and for manual (no key order in JSON)', () => {
    expect(sortGroups(groups, 'worst')).toBe(groups)
    expect(sortGroups(groups, 'manual')).toBe(groups)
    expect(sortGroups(groups, undefined)).toBe(groups)
  })
  it('last_activity puts the freshest first and the unknown last', () => {
    expect(sortGroups(groups, 'last_activity').map((x) => x.slug)).toEqual(['c', 'a', 'b'])
    expect(groups.map((x) => x.slug)).toEqual(['a', 'b', 'c'])
  })
})

describe('asleepProjects / groupTarget', () => {
  const p = (slug: string, archived?: boolean) =>
    ({ slug, name: slug, archived, group: slug, group_name: slug }) as unknown as Project
  it('lists the archived slugs only', () => {
    expect(asleepProjects([p('a'), p('b', true), p('c', true)])).toEqual(['b', 'c'])
    expect(asleepProjects(undefined)).toEqual([])
  })
  it('a group opens its first repo until the group page exists', () => {
    expect(groupTarget({ slug: 'acme', projects: ['acme-api', 'acme-zap'] })).toEqual({
      to: '/p/$slug',
      params: { slug: 'acme-api' },
    })
    expect(groupTarget({ slug: 'solo', projects: [] }).params.slug).toBe('solo')
  })
})
