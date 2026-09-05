import { describe, expect, it } from 'vitest'

import type { CockpitConfig, GroupSummary, Project } from '../api/types'
import {
  formFromConfig,
  groupRows,
  manualOrder,
  manualSidebarOrder,
  parseChannels,
  patchFromForm,
  slackMappingOf,
  slackRows,
  sleepRows,
} from './settingsView'

const config: CockpitConfig = {
  groups: [
    { slug: 'atlas', name: 'orbit', order: 1 },
    { slug: 'acme', name: 'acme', order: 0 },
  ],
  doing_idle_days: 7,
  waiting_highlight_days: 5,
  stuck_project_days: 14,
  cutoff_hour: 18,
  refresh: { every_seconds: 1800, window: '07:00-20:00' },
  sections: { needs_me: true, recent: false },
  sources: { pm: true, git: true, slack: false },
  sidebar: { variant: 'columns', show_repos: true, sort: 'worst', width: 0 },
  git: { all_branches: false },
}

describe('form and patch', () => {
  it('the form mirrors the config; an unchanged form is no patch', () => {
    const f = formFromConfig(config)
    expect(f.refresh_minutes).toBe(30)
    expect(f.groups).toEqual({
      orbit: { name: 'orbit', order: 1 },
      acme: { name: '', order: 0 },
    })
    expect(patchFromForm(f, formFromConfig(config))).toBeNull()
  })
  it('only the changed fields travel, toggles and groups key by key', () => {
    const base = formFromConfig(config)
    const edited: typeof base = {
      ...base,
      cutoff_hour: 20,
      refresh_minutes: 10,
      window: ' 08:00-19:00 ',
      sections: { ...base.sections, recent: true },
      sources: { ...base.sources },
      sidebar: { ...base.sidebar, variant: 'rail' },
      git_all_branches: true,
      groups: { ...base.groups, acme: { name: 'ACME', order: 2 }, vega: { name: '', order: 3 } },
    }
    expect(patchFromForm(base, edited)).toEqual({
      cutoff_hour: 20,
      refresh: { every_seconds: 600, window: '08:00-19:00' },
      sections: { recent: true },
      sidebar: { variant: 'rail' },
      git: { all_branches: true },
      groups: [
        { slug: 'acme', name: 'ACME', order: 2 },
        { slug: 'vega', order: 3 },
      ],
    })
  })
})

describe('rows', () => {
  const groups = [
    { slug: 'alpha', name: 'alpha', projects: ['alpha'] },
    { slug: 'acme', name: 'acme', projects: ['acme-api', 'acme-zap'] },
  ]
  it('group rows: every derived group plus configured-but-asleep ones, in manual order', () => {
    const rows = groupRows(groups, formFromConfig(config), config.groups)
    expect(rows.map((r) => [r.slug, r.name, r.order, r.members, r.configured])).toEqual([
      ['atlas', 'orbit', 1, [], true],
      ['alpha', 'alpha', 0, ['alpha'], false],
      ['acme', 'acme', 0, ['acme-api', 'acme-zap'], true],
    ])
  })
  it('manual order: placed by order then slug, unplaced after by slug', () => {
    expect(
      manualOrder([
        { slug: 'z', order: 0 },
        { slug: 'b', order: 2 },
        { slug: 'a', order: 0 },
        { slug: 'c', order: 1 },
      ]).map((r) => r.slug),
    ).toEqual(['c', 'b', 'a', 'z'])
  })
  it('the sidebar manual sort follows the config array, unknown groups keep the API order after it', () => {
    const g = (slug: string): GroupSummary => ({
      slug,
      name: slug,
      projects: [slug],
      worst: 'ok',
      failed: 0,
      visual: 0,
      waiting: 0,
      quiet: 0,
    })
    const sorted = manualSidebarOrder([g('x'), g('acme'), g('y'), g('atlas')], config.groups)
    expect(sorted.map((s) => s.slug)).toEqual(['atlas', 'acme', 'x', 'y'])
    expect(manualSidebarOrder([g('x'), g('y')], undefined).map((s) => s.slug)).toEqual(['x', 'y'])
  })
  it('slack rows and channel parsing', () => {
    const projects: Project[] = [
      {
        slug: 'acme-api',
        name: 'ACME-API',
        group: 'acme',
        group_name: 'ACME',
        task_counts: {},
        statuses: [],
        landing_statuses: [],
        slack: { workspace: 'acme', channels: ['#acme-api-dev', 'general'] },
      },
      {
        slug: 'orbit2',
        name: 'Orbit2',
        group: 'orbit2',
        group_name: 'orbit2',
        task_counts: {},
        statuses: [],
        landing_statuses: [],
        archived: true,
      },
      {
        slug: 'alpha',
        name: 'Alpha',
        group: 'alpha',
        group_name: 'alpha',
        task_counts: {},
        statuses: [],
        landing_statuses: [],
      },
    ]
    expect(slackRows(projects)).toEqual([
      { slug: 'acme-api', name: 'ACME-API', workspace: 'acme', channels: '#acme-api-dev, general' },
      { slug: 'alpha', name: 'Alpha', workspace: '', channels: '' },
    ])
    expect(parseChannels(' #a, b #c,, ')).toEqual(['#a', 'b', '#c'])
    expect(slackMappingOf({ workspace: ' acme ', channels: '#a b' })).toEqual({
      workspace: 'acme',
      channels: ['#a', 'b'],
    })
    expect(sleepRows(projects).asleep.map((p) => p.slug)).toEqual(['orbit2'])
    expect(sleepRows(projects).awake.map((p) => p.slug)).toEqual(['acme-api', 'alpha'])
  })
})
