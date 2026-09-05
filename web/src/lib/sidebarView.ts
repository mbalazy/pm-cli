import type { GroupSummary, Project, SidebarConfig } from '../api/types'

// The sidebar's shape is CONFIG (cockpit.sidebar, read off /api/config), not
// a UI toggle. This file turns the config into the flags the component
// renders and applies the one sort the API does not already apply.

export interface SidebarLayout {
  variant: 'columns' | 'plain' | 'rail'
  /** The four counter columns (columns variant only). */
  showCounts: boolean
  /** Group names (everything but the rail, which keeps them in a tooltip). */
  showNames: boolean
  /** Member repos under a multi-repo group. */
  showRepos: boolean
  /** CSS width, or undefined for the stylesheet's default. */
  width?: string
}

const DEFAULT: SidebarConfig = { variant: 'columns', show_repos: true, sort: 'worst', width: 0 }

/** Flags off the config; a missing config (still loading, a 500) is the default layout. */
export function sidebarLayout(cfg: SidebarConfig | undefined): SidebarLayout {
  const c = cfg ?? DEFAULT
  const variant: SidebarLayout['variant'] =
    c.variant === 'plain' || c.variant === 'rail' ? c.variant : 'columns'
  return {
    variant,
    showCounts: variant === 'columns',
    showNames: variant !== 'rail',
    showRepos: variant !== 'rail' && c.show_repos,
    width: c.width > 0 ? `${c.width}px` : undefined,
  }
}

/**
 * Orders the groups per `cockpit.sidebar.sort`. `worst` is the API's own
 * order (worst severity first), so it is returned as is. `last_activity`
 * puts the freshest group first, groups with no activity last. `manual` (the
 * order of `cockpit.groups` in config.yaml) cannot be recovered from JSON,
 * whose object keys carry no order - it falls back to the API order until
 * the config endpoint carries an explicit order.
 */
export function sortGroups(groups: GroupSummary[], sort: string | undefined): GroupSummary[] {
  if (sort !== 'last_activity') return groups
  return [...groups].sort((a, b) => {
    const x = a.last_activity ?? ''
    const y = b.last_activity ?? ''
    if (x === y) return 0
    if (x === '') return 1
    if (y === '') return -1
    return x < y ? 1 : -1
  })
}

/** Asleep projects (archived) - shown as one "asleep (N)" line, never as rows. */
export function asleepProjects(projects: Project[] | undefined): string[] {
  return (projects ?? []).filter((p) => p.archived).map((p) => p.slug)
}

/** Where a group link goes: the group page. */
export function groupTarget(group: Pick<GroupSummary, 'slug'>): {
  to: '/g/$group'
  params: { group: string }
} {
  return { to: '/g/$group', params: { group: group.slug } }
}

/** Where a member repo's line goes: the group's board tab, on that repo. */
export function repoTarget(
  group: string,
  slug: string,
): { to: '/g/$group'; params: { group: string }; search: { tab: 'board'; repo: string } } {
  return { to: '/g/$group', params: { group }, search: { tab: 'board', repo: slug } }
}
