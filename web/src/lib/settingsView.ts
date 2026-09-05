import type {
  CockpitConfig,
  ConfigGroup,
  GroupSummary,
  Project,
  ProjectGroup,
  SettingsPatch,
  SlackMapping,
} from '../api/types'

// The settings screen's rules: the editable copy of the config, the diff
// that becomes the PATCH (only what changed - the API keeps the rest), the
// group and Slack rows. The values, their defaults and their validation are
// the server's (/api/config resolves the defaults; POST /api/settings
// rejects a bad value naming the field); this file never invents either.

/** The form's flat, editable copy of the cockpit block. Strings where an input needs them. */
export interface SettingsForm {
  doing_idle_days: number
  waiting_highlight_days: number
  stuck_project_days: number
  cutoff_hour: number
  refresh_minutes: number
  window: string
  sections: Record<string, boolean>
  sources: Record<string, boolean>
  sidebar: { variant: string; show_repos: boolean; sort: string; width: number }
  git_all_branches: boolean
  report_model: string
  report_language: string
  /** The configured groups: name + order per slug (unknown groups come from `groupRows`). */
  groups: Record<string, { name: string; order: number }>
}

export function formFromConfig(c: CockpitConfig): SettingsForm {
  const groups: SettingsForm['groups'] = {}
  for (const g of c.groups)
    groups[g.slug] = { name: g.name === g.slug ? '' : g.name, order: g.order }
  return {
    doing_idle_days: c.doing_idle_days,
    waiting_highlight_days: c.waiting_highlight_days,
    stuck_project_days: c.stuck_project_days,
    cutoff_hour: c.cutoff_hour,
    refresh_minutes: Math.round(c.refresh.every_seconds / 60),
    window: c.refresh.window,
    sections: { ...c.sections },
    sources: { ...c.sources },
    sidebar: { ...c.sidebar },
    git_all_branches: c.git.all_branches,
    report_model: c.report.model,
    report_language: c.report.language,
    groups,
  }
}

/**
 * The PATCH between the loaded form and the edited one - every field that
 * differs, nothing else - or null when nothing changed. Toggle maps and
 * groups are diffed key by key, since the API merges them.
 */
export function patchFromForm(base: SettingsForm, edited: SettingsForm): SettingsPatch | null {
  const p: SettingsPatch = {}
  if (edited.doing_idle_days !== base.doing_idle_days) p.doing_idle_days = edited.doing_idle_days
  if (edited.waiting_highlight_days !== base.waiting_highlight_days)
    p.waiting_highlight_days = edited.waiting_highlight_days
  if (edited.stuck_project_days !== base.stuck_project_days)
    p.stuck_project_days = edited.stuck_project_days
  if (edited.cutoff_hour !== base.cutoff_hour) p.cutoff_hour = edited.cutoff_hour
  const refresh: NonNullable<SettingsPatch['refresh']> = {}
  if (edited.refresh_minutes !== base.refresh_minutes)
    refresh.every_seconds = edited.refresh_minutes * 60
  if (edited.window.trim() !== base.window) refresh.window = edited.window.trim()
  if (Object.keys(refresh).length > 0) p.refresh = refresh
  const sections = diffToggles(base.sections, edited.sections)
  if (sections) p.sections = sections
  const sources = diffToggles(base.sources, edited.sources)
  if (sources) p.sources = sources
  const sidebar: NonNullable<SettingsPatch['sidebar']> = {}
  if (edited.sidebar.variant !== base.sidebar.variant) sidebar.variant = edited.sidebar.variant
  if (edited.sidebar.show_repos !== base.sidebar.show_repos)
    sidebar.show_repos = edited.sidebar.show_repos
  if (edited.sidebar.sort !== base.sidebar.sort) sidebar.sort = edited.sidebar.sort
  if (edited.sidebar.width !== base.sidebar.width) sidebar.width = edited.sidebar.width
  if (Object.keys(sidebar).length > 0) p.sidebar = sidebar
  if (edited.git_all_branches !== base.git_all_branches)
    p.git = { all_branches: edited.git_all_branches }
  const report: NonNullable<SettingsPatch['report']> = {}
  if (edited.report_model.trim() !== base.report_model) report.model = edited.report_model.trim()
  if (edited.report_language.trim() !== base.report_language)
    report.language = edited.report_language.trim()
  if (Object.keys(report).length > 0) p.report = report
  const groups: NonNullable<SettingsPatch['groups']> = []
  for (const [slug, g] of Object.entries(edited.groups)) {
    const b = base.groups[slug] ?? { name: '', order: 0 }
    const entry: { slug: string; name?: string; order?: number } = { slug }
    if (g.name.trim() !== b.name) entry.name = g.name.trim()
    if (g.order !== b.order) entry.order = g.order
    if (entry.name !== undefined || entry.order !== undefined) groups.push(entry)
  }
  if (groups.length > 0) p.groups = groups
  return Object.keys(p).length === 0 ? null : p
}

function diffToggles(
  base: Record<string, boolean>,
  edited: Record<string, boolean>,
): Record<string, boolean> | null {
  const out: Record<string, boolean> = {}
  for (const [k, v] of Object.entries(edited)) if (base[k] !== v) out[k] = v
  return Object.keys(out).length === 0 ? null : out
}

/** One row of the Groups section: every group the projects form, with its configured name/order and members. */
export interface GroupRow {
  slug: string
  /** The display name the sidebar shows (configured or the slug). */
  name: string
  order: number
  members: string[]
  /** True when the config names it (a rename or an order is stored). */
  configured: boolean
}

export function groupRows(
  groups: ProjectGroup[] | undefined,
  form: SettingsForm,
  config: ConfigGroup[] | undefined,
): GroupRow[] {
  const byGroup = new Map<string, GroupRow>()
  for (const g of groups ?? []) {
    const f = form.groups[g.slug]
    byGroup.set(g.slug, {
      slug: g.slug,
      name: f?.name || g.name,
      order: f?.order ?? 0,
      members: g.projects,
      configured: f !== undefined,
    })
  }
  // A configured group whose members all sleep is still a setting - shown
  // without members so the entry can be seen and edited.
  for (const c of config ?? []) {
    if (byGroup.has(c.slug)) continue
    const f = form.groups[c.slug] ?? { name: c.name === c.slug ? '' : c.name, order: c.order }
    byGroup.set(c.slug, {
      slug: c.slug,
      name: f.name || c.slug,
      order: f.order,
      members: [],
      configured: true,
    })
  }
  return manualOrder([...byGroup.values()])
}

/** Placed rows (order > 0) by order then slug, then the unplaced by slug - the API's GroupOrder rule. */
export function manualOrder<T extends { slug: string; order: number }>(rows: T[]): T[] {
  return [...rows].sort((a, b) => {
    if (a.order !== b.order) {
      if (a.order === 0) return 1
      if (b.order === 0) return -1
      return a.order - b.order
    }
    return a.slug < b.slug ? -1 : a.slug > b.slug ? 1 : 0
  })
}

/**
 * The sidebar's `manual` sort: the config's order (the /api/config groups
 * array IS that order), then the groups the config does not name, in the
 * API's own order.
 */
export function manualSidebarOrder(
  groups: GroupSummary[],
  config: ConfigGroup[] | undefined,
): GroupSummary[] {
  const rank = new Map<string, number>()
  for (const [i, c] of (config ?? []).entries()) rank.set(c.slug, i)
  return [...groups]
    .map((g, i) => ({ g, key: rank.has(g.slug) ? rank.get(g.slug)! : (config?.length ?? 0), i }))
    .sort((a, b) => a.key - b.key || a.i - b.i)
    .map((x) => x.g)
}

/** One row of the Slack section: a project with its mapping as editable text. */
export interface SlackRow {
  slug: string
  name: string
  workspace: string
  channels: string
}

export function slackRows(projects: Project[] | undefined): SlackRow[] {
  return (projects ?? [])
    .filter((p) => !p.archived)
    .map((p) => ({
      slug: p.slug,
      name: p.name,
      workspace: p.slack?.workspace ?? '',
      channels: (p.slack?.channels ?? []).join(', '),
    }))
}

/** "#a, b #c" -> ['#a', 'b', '#c']: commas and whitespace separate, blanks drop. */
export function parseChannels(text: string): string[] {
  return text
    .split(/[,\s]+/)
    .map((c) => c.trim())
    .filter((c) => c !== '')
}

/** The mapping a Slack row's edit sends (an empty one removes the mapping server-side). */
export function slackMappingOf(row: Pick<SlackRow, 'workspace' | 'channels'>): SlackMapping {
  return { workspace: row.workspace.trim(), channels: parseChannels(row.channels) }
}

/** Asleep and awake projects for the Asleep section. */
export function sleepRows(projects: Project[] | undefined): {
  asleep: Project[]
  awake: Project[]
} {
  const all = projects ?? []
  return { asleep: all.filter((p) => p.archived), awake: all.filter((p) => !p.archived) }
}

/** The feed sources in the registry's order (storage.CockpitSources), with the settings labels. */
export const SOURCE_ORDER: readonly { name: string; label: string }[] = [
  { name: 'pm', label: 'pm' },
  { name: 'git', label: 'git' },
  { name: 'github', label: 'github (gh)' },
  { name: 'slack', label: 'slack (MCP)' },
  { name: 'report', label: 'report (LLM)' },
]

export const SIDEBAR_VARIANTS = ['columns', 'plain', 'rail'] as const
export const SIDEBAR_SORTS = ['worst', 'last_activity', 'manual'] as const

/** The needs-me rank, read-only in v1 (the aggregation's rule, worded once here). */
export const NEEDS_ME_ORDER =
  'failed › visual claims › landed without acceptance › partial › live claim'
