import { QueryClient } from '@tanstack/react-query'
import { createMemoryHistory } from '@tanstack/react-router'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from './App'

// The read-only shell end to end: real router, real hooks, fake /api.

const projects = {
  projects: [
    {
      slug: 'alpha',
      name: 'Alpha',
      group: 'alpha',
      group_name: 'ALPHA',
      task_counts: { todo: 2 },
      statuses: ['todo', 'done'],
      landing_statuses: [],
    },
    {
      slug: 'acme-api',
      name: 'ACME-API',
      group: 'acme',
      group_name: 'ACME',
      stack: 'Livingdocs',
      notes: 'ACME-60 in two repos',
      task_counts: { doing: 1, waiting: 1 },
      statuses: ['todo', 'doing', 'waiting', 'done'],
      landing_statuses: [],
    },
    {
      slug: 'acme-zap',
      name: 'acme-zap',
      group: 'acme',
      group_name: 'ACME',
      task_counts: { doing: 1 },
      statuses: ['todo', 'doing', 'done'],
      landing_statuses: [],
    },
  ],
}
const summary = (id: string, title: string) => ({
  id,
  title,
  status: 'todo',
  project: 'alpha',
  updated: '2026-01-01',
  session_count: 0,
})
const tasks = {
  tasks: [summary('alpha-1', 'First'), summary('alpha-2', 'Second')],
  total: 2,
  shown: 2,
}
const acme-apiTasks = {
  tasks: [
    { ...summary('acme-api-1', 'Acme-api thing'), project: 'acme-api', status: 'waiting' },
    { ...summary('acme-api-2', 'Old doing'), project: 'acme-api', status: 'doing', updated: '2025-12-01' },
  ],
  total: 2,
  shown: 2,
}
const acme-zapTasks = {
  tasks: [
    {
      ...summary('acme-zap-1', 'Zap task'),
      project: 'acme-zap',
      status: 'doing',
      updated: '2026-01-05T10:00:00Z',
    },
  ],
  total: 1,
  shown: 1,
}
const context = (slug: string, trackers: unknown[]) => ({
  project: { slug, name: slug, statuses: [] },
  doing_tasks: [],
  task_counts: {},
  trackers,
})
const tracker = (id: string, title: string, total: number, progress: Record<string, number>) => ({
  id,
  title,
  status: 'doing',
  total,
  progress,
})
const detail = {
  ...summary('alpha-1', 'First'),
  created: '2026-01-01',
  status_changed: '',
  branch: 'feat/x',
  links: { pr: 'https://example.com/pr/1' },
  body: '<!-- spec:start -->\n## Description\n\ncurrent truth\n<!-- spec:end -->\n\n2026-01-02: a log line\n',
}
const runs = {
  rows: [
    {
      project: 'alpha',
      tracker: 'alpha-9',
      title: 'Epic',
      status: 'doing',
      updated: '2026-01-02T09:30:00Z',
      run: { state: 'running', done: 1, total: 3 },
      acceptance: {},
      run_started: '2026-01-02T08:00:00Z',
      run_updated: '2026-01-02T09:30:00Z',
      run_live: true,
    },
    {
      project: 'alpha',
      tracker: 'alpha-1',
      title: 'First',
      status: 'todo',
      updated: '2026-01-01T12:00:00Z',
      run: { state: 'failed', done: 0, total: 2 },
      acceptance: {},
      run_started: '2026-01-01T10:00:00Z',
      run_updated: '2026-01-01T12:00:00Z',
    },
    {
      project: 'acme-api',
      tracker: 'acme-api-9',
      title: 'Old epic',
      status: 'done',
      updated: '2025-12-01T12:00:00Z',
      run: { state: 'done', done: 2, total: 2 },
      acceptance: { state: 'done' },
      run_started: '2025-12-01T10:00:00Z',
      run_updated: '2025-12-01T12:00:00Z',
    },
  ],
}
const remoteRuns = {
  rows: [
    ...runs.rows,
    {
      remote: 'vps',
      project: 'beta',
      tracker: 'beta-1',
      title: 'Remote epic',
      run: { state: 'done', done: 2, total: 2 },
      acceptance: { state: 'done' },
    },
  ],
}

/** Every POST the fake saw: method, path, headers and the parsed body. */
interface SeenPost {
  path: string
  header: string | null
  body: unknown
}
const posts: SeenPost[] = []

function fakeFetch(routes: Record<string, unknown>) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (init?.method === 'POST') {
      const headers = new Headers(init.headers)
      posts.push({
        path: url,
        header: headers.get('X-PM-Client'),
        body: init.body ? JSON.parse(init.body as string) : undefined,
      })
      if (url.includes('/t-fail')) {
        return new Response(JSON.stringify({ error: 'status "bogus" is not one of todo, done' }), {
          status: 400,
        })
      }
      if (url.includes('/api/report')) {
        return new Response(
          JSON.stringify({
            state: 'writing',
            enabled: true,
            period: 'p',
            cutoff: '',
            model: 'haiku',
          }),
          { status: 200 },
        )
      }
      if (url.includes('/api/runs/')) {
        return new Response(
          JSON.stringify({
            pid: 4242,
            log: '/pm/alpha/.executor/alpha-9.log',
            kind: 'run-epic',
            argv: [],
          }),
          { status: 200 },
        )
      }
      return new Response(JSON.stringify({ ok: true, task_ids: [], focused: true }), {
        status: 200,
      })
    }
    const [path, qs] = url.split('?')
    // An exact `path?query` route wins; otherwise the bare path answers.
    const key = qs && routes[`${path}?${qs}`] !== undefined ? `${path}?${qs}` : path
    const body = routes[key]
    if (body === undefined) {
      return new Response(JSON.stringify({ error: `no such endpoint: ${key}` }), { status: 404 })
    }
    return new Response(JSON.stringify(body), { status: 200 })
  })
}

function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <App history={createMemoryHistory({ initialEntries: [path] })} queryClient={queryClient} />,
  )
}

const group = (slug: string, worst: string, waiting: number) => ({
  slug,
  name: slug.toUpperCase(),
  projects: slug === 'acme' ? ['acme-api', 'acme-zap'] : [slug],
  worst,
  failed: worst === 'crit' ? 1 : 0,
  visual: 0,
  waiting,
  quiet: 0,
  last_activity: slug === 'alpha' ? '2026-01-02T00:00:00Z' : '2026-01-05T00:00:00Z',
})
const row = (section: string, project: string, id: string, title: string, extra = {}) => ({
  section,
  severity: 'info',
  project,
  group: project === 'acme-api' ? 'acme' : project,
  task_id: id,
  title,
  reason: `${section} reason`,
  age_seconds: null,
  actions: ['open', 'focus_toggle'],
  ...extra,
})
const attention = {
  generated: '2026-01-02T10:00:00Z',
  wip: 3,
  sections: [
    {
      name: 'needs_me',
      rows: [
        row('needs_me', 'alpha', 'alpha-1', 'First', { severity: 'crit', age_seconds: 90000 }),
      ],
      total: 1,
    },
    { name: 'in_progress', rows: [], total: 0 },
    {
      name: 'waiting',
      rows: [
        row('waiting', 'acme-api', 'acme-api-1', 'Acme-api thing', {
          flags: ['no_reason'],
          actions: ['open', 'focus_toggle', 'set_waiting_for', 'back_to_todo'],
        }),
        row('waiting', 'alpha', 'alpha-2', 'Second', {
          actions: ['open', 'focus_toggle', 'set_waiting_for', 'back_to_todo'],
        }),
      ],
      total: 2,
    },
    {
      name: 'changes',
      rows: [row('changes', 'alpha', 'alpha-1', 'First moved', { actions: ['mark_seen', 'open'] })],
      total: 44,
    },
  ],
  groups: [group('alpha', 'crit', 1), group('acme', 'warn', 1)],
}
/** The queue with a failed epic run carrying the run-control actions. */
const attentionRuns = {
  ...attention,
  sections: attention.sections.map((s) =>
    s.name === 'needs_me'
      ? {
          ...s,
          rows: [
            ...s.rows,
            row('needs_me', 'alpha', 'alpha-9', 'Epic', {
              severity: 'crit',
              reason: 'run failed',
              actions: ['open', 'resume_run', 'kill'],
            }),
          ],
          total: 2,
        }
      : s,
  ),
}
const attentionNzz = {
  ...attention,
  sections: attention.sections.map((s) => ({
    ...s,
    rows: s.rows.filter((r) => r.group === 'acme'),
    total: s.rows.filter((r) => r.group === 'acme').length,
  })),
  groups: [group('acme', 'warn', 1)],
}
const config = (sidebar: Record<string, unknown>) => ({
  cockpit: {
    groups: [],
    doing_idle_days: 7,
    waiting_highlight_days: 5,
    stuck_project_days: 14,
    cutoff_hour: 18,
    refresh: { every_seconds: 1800, window: '07:00-20:00' },
    sections: {},
    sources: {},
    sidebar: { variant: 'columns', show_repos: true, sort: 'worst', width: 0, ...sidebar },
    show_executor: true,
    git: { all_branches: false },
    report: { model: 'haiku', language: 'pl' },
    slack: { workspaces: [] },
  },
})
const settingsConfig = {
  cockpit: {
    ...config({}).cockpit,
    groups: [{ slug: 'acme', name: 'ACME', order: 1 }],
    sections: { needs_me: true, waiting: true, recent: false },
    sources: { pm: true, git: true, github: false, slack: false, report: false },
  },
}
const groupsApi = {
  groups: [
    { slug: 'alpha', name: 'alpha', projects: ['alpha'] },
    { slug: 'acme', name: 'ACME', projects: ['acme-api', 'acme-zap'] },
  ],
}
const changes = {
  cutoff: '2026-01-01T18:00:00Z',
  events: [
    {
      id: 'e1',
      ts: '2026-01-02T09:00:00Z',
      source: 'pm',
      project: 'acme-api',
      group: 'acme',
      task_id: 'acme-api-1',
      title: 'Acme-api thing',
      detail: 'moved',
      severity: 'ok',
      seen: false,
    },
    {
      id: 'e2',
      ts: '2026-01-02T09:10:00Z',
      source: 'git',
      project: 'alpha',
      group: 'alpha',
      title: 'alpha event',
      severity: 'info',
      seen: false,
    },
  ],
  unseen: 2,
  sources: [
    { name: 'pm', enabled: true, events: 1, last_fetch: '2026-01-02T09:30:00Z' },
    { name: 'git', enabled: true, events: 1, error: 'gh: not logged in' },
    { name: 'slack', enabled: false, events: 0 },
  ],
}

const focusPlan = { date: '2026-01-02', task_ids: [] as string[], tasks: [] as unknown[] }
const reportOff = {
  enabled: false,
  period: '2026-01-01T18',
  cutoff: '2026-01-01T18:00:00Z',
  state: 'off',
  model: 'haiku',
}
const reportDone = {
  enabled: true,
  period: '2026-01-01T18',
  cutoff: '2026-01-01T18:00:00Z',
  state: 'done',
  model: 'haiku',
  report: {
    period: '2026-01-01T18',
    cutoff: '2026-01-01T18:00:00Z',
    generated: '2026-01-02T08:00:00Z',
    model: 'haiku',
    tokens: { input: 100, cache_creation: 0, cache_read: 4000, output: 300 },
    duration_s: 9,
    text: '**ACME**: acme-api-1 waits on review.\n\nDecide on the batch.',
    suggestions: [
      {
        id: 'sg1',
        project: 'acme-api',
        task_id: 'acme-api-1',
        action: 'back_to_todo',
        text: 'the reviewer answered',
      },
      { id: 'sg2', task_id: 'zzz-9', text: 'nothing known' },
    ],
    dismissed: [] as string[],
    events: 2,
    rows: 1,
  },
}

const api = {
  '/api/focus': focusPlan,
  '/api/projects': projects,
  '/api/tasks': tasks,
  '/api/tasks/alpha/alpha-1': detail,
  '/api/runs': runs,
  '/api/runs?remote=1': remoteRuns,
  '/api/attention': attention,
  '/api/attention?group=acme': attentionNzz,
  '/api/tasks?project=acme-api&limit=200': acme-apiTasks,
  '/api/tasks?project=acme-zap&limit=200': acme-zapTasks,
  '/api/context?project=acme-api': context('acme-api', [tracker('acme-api-9', 'Old epic', 2, { done: 2 })]),
  '/api/context?project=acme-zap': context('acme-zap', [
    tracker('acme-zap-1', 'Zap task', 2, { done: 1, doing: 1 }),
  ]),
  '/api/config': config({}),
  '/api/changes': changes,
  '/api/groups': groupsApi,
  '/api/report': reportOff,
  '/api/solo': { shifts: [] },
  '/api/runs/alpha/alpha-9/plan?action=resume_run': {
    action: 'resume_run',
    project: 'alpha',
    task_id: 'alpha-9',
    kind: 'run-epic',
    argv: ['run-epic', 'alpha', 'alpha-9'],
    cwd: '/repo/alpha',
    log: '/pm/alpha/.executor/alpha-9.log',
    warnings: ['a run of this task is already in flight'],
    additional_avail: false,
  },
  '/api/runs/alpha/alpha-9/plan?action=resume_run&yolo=1': {
    action: 'resume_run',
    project: 'alpha',
    task_id: 'alpha-9',
    kind: 'run-epic',
    argv: ['run-epic', 'alpha', 'alpha-9', '--yolo'],
    cwd: '/repo/alpha',
    log: '/pm/alpha/.executor/alpha-9.log',
    additional_avail: false,
  },
  '/api/runs/alpha/alpha-9/plan?action=kill': {
    action: 'kill',
    project: 'alpha',
    task_id: 'alpha-9',
    kind: 'run-epic',
    target: 'run-epic pid 777 (started 2026-01-02T08:00:00Z)',
    pid: 777,
    additional_avail: false,
  },
}

afterEach(() => {
  vi.unstubAllGlobals()
  posts.length = 0
})

describe('task detail', () => {
  it('renders the Spec and Log zones under their headings, with the fields', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/p/alpha/t/alpha-1')
    const article = await screen.findByRole('article')
    expect(within(article).getByRole('heading', { level: 1 })).toHaveTextContent('First')
    expect(within(article).getByText('Spec (current truth)')).toBeInTheDocument()
    expect(within(article).getByText('current truth')).toBeInTheDocument()
    expect(within(article).getByText('Log (history, append-only)')).toBeInTheDocument()
    expect(within(article).getByText('2026-01-02: a log line')).toBeInTheDocument()
    expect(within(article).getByRole('link', { name: 'pr' })).toHaveAttribute(
      'href',
      'https://example.com/pr/1',
    )
    expect(within(article).getByText(/status changed unknown/)).toBeInTheDocument()
  })
})

describe('today', () => {
  it('renders the sections in the API order, an empty one as a sentence, the changes rest as elsewhere', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/')
    const needs = await screen.findByRole('region', { name: 'Needs me' })
    const regions = screen.getAllByRole('region').map((r) => r.getAttribute('data-section'))
    expect(regions).toEqual(['needs_me', 'in_progress', 'waiting', 'changes'])
    expect(within(needs).getByText('First')).toBeInTheDocument()
    expect(within(needs).getByText('1d')).toBeInTheDocument()
    expect(within(needs).getByLabelText('severity: crit')).toHaveTextContent('✗')
    const progress = screen.getByRole('region', { name: 'In progress' })
    expect(within(progress).getByText('Nothing is running.')).toBeInTheDocument()
    expect(within(progress).queryByRole('list')).toBeNull()
    const waiting = screen.getByRole('region', { name: 'Waiting on' })
    expect(within(waiting).getAllByText('since ?')).toHaveLength(2)
    expect(within(waiting).getByText('no reason')).toBeInTheDocument()
    const ch = screen.getByRole('region', { name: 'Changes since the cutoff' })
    expect(within(ch).getByText('1 of 44')).toBeInTheDocument()
    expect(within(ch).getByText(/\+43 more/)).toBeInTheDocument()
    expect(screen.getByText('WIP 3')).toBeInTheDocument()
    expect(screen.getByText(/refreshed \d\d:\d\d · next \d\d:\d\d/)).toBeInTheDocument()
  })

  it('renders the action buttons the row lists: open is a link, focus is live, kill is absent', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/')
    const needs = await screen.findByRole('region', { name: 'Needs me' })
    const item = within(needs).getByRole('listitem')
    expect(within(item).getByRole('link', { name: 'open' })).toHaveAttribute(
      'href',
      '/p/alpha/t/alpha-1',
    )
    expect(within(item).getByRole('button', { name: 'focus' })).toBeEnabled()
    expect(within(item).queryByRole('button', { name: 'kill' })).toBeNull()
  })

  it('the group filter lives in the URL and narrows every section', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/')
    await screen.findByRole('region', { name: 'Needs me' })
    const chips = screen.getAllByRole('navigation', { name: 'Group filter' })[0]
    // The phone strip (the first nav, compact) also links to the group page itself.
    expect(within(chips).getByRole('link', { name: 'open ALPHA' })).toHaveAttribute(
      'href',
      '/g/alpha?tab=overview',
    )
    await user.click(within(chips).getByRole('link', { name: 'ACME' }))
    const waiting = await screen.findByRole('region', { name: 'Waiting on' })
    await vi.waitFor(() => expect(within(waiting).queryByText('Second')).toBeNull())
    expect(within(waiting).getByText('Acme-api thing')).toBeInTheDocument()
    expect(
      within(screen.getByRole('region', { name: 'Needs me' })).getByText('Nothing needs you.'),
    ).toBeInTheDocument()
    expect(within(chips).getByRole('link', { current: 'page' })).toHaveTextContent('ACME')
    // The chord: g then the group's letter; `a` is alpha's.
    await user.keyboard('ga')
    await vi.waitFor(() =>
      expect(within(chips).getByRole('link', { current: 'page' })).toHaveTextContent('ALPHA'),
    )
    expect(
      await within(screen.getByRole('region', { name: 'Waiting on' })).findByText('Second'),
    ).toBeInTheDocument()
  })

  it('1 / 3 switch screens, j/k walk the rows, Enter opens the selected task', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/runs')
    await screen.findByRole('table', { name: 'Runs' })
    await user.keyboard('1')
    await screen.findByRole('region', { name: 'Needs me' })
    await user.keyboard('j')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('First')
    await user.keyboard('j')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('Acme-api thing')
    await user.keyboard('k')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('First')
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('article')).toBeInTheDocument()
    await user.keyboard('3')
    expect(await screen.findByRole('table', { name: 'Runs' })).toBeInTheDocument()
  })
})

describe('mutations go through the dialog', () => {
  it('a row action opens the dialog; Esc cancels without a request', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/')
    const needs = await screen.findByRole('region', { name: 'Needs me' })
    await user.click(within(needs).getByRole('button', { name: 'focus' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    expect(within(dialog).getByRole('heading')).toHaveTextContent('alpha-1 First')
    expect(within(dialog).getByText(/Puts alpha-1 on today's focus/)).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await vi.waitFor(() => expect(dialog).not.toHaveAttribute('open'))
    expect(posts).toHaveLength(0)
  })

  it('t on the selected row asks too, and Enter posts focus/toggle with the client header', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/')
    await screen.findByRole('region', { name: 'Needs me' })
    await user.keyboard('j')
    await user.keyboard('t')
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    await user.click(within(dialog).getByRole('button', { name: 'add to focus' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/focus/toggle',
      header: 'cockpit',
      body: { task_id: 'alpha-1' },
    })
    await vi.waitFor(() => expect(dialog).not.toHaveAttribute('open'))
    expect(await screen.findByRole('status')).toHaveTextContent('saved')
  })

  it('waiting without a reason warns; Enter in the field sends status + reason', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/')
    const waiting = await screen.findByRole('region', { name: 'Waiting on' })
    const second = within(waiting).getAllByRole('listitem')[1]
    await user.click(within(second).getByRole('button', { name: 'waiting…' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/no reason/)
    await user.type(within(dialog).getByRole('textbox'), 'review by Marta{Enter}')
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0].path).toBe('/api/tasks/alpha/alpha-2')
    expect(posts[0].body).toEqual({ status: 'waiting', waiting_for: 'review by Marta' })
  })

  it('Esc inside a detail dialog closes the dialog only - the task stays open', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/p/alpha/t/alpha-1')
    const article = await screen.findByRole('article')
    await user.click(within(article).getByRole('button', { name: 'write brief' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    await user.keyboard('{Escape}')
    await vi.waitFor(() => expect(dialog).not.toHaveAttribute('open'))
    // Still the detail (memory history: assert the screen, not window.location).
    expect(screen.getByRole('article')).toBeInTheDocument()
    expect(
      within(screen.getByRole('article')).getByRole('heading', { level: 1 }),
    ).toHaveTextContent('First')
    expect(posts).toHaveLength(0)
  })

  it('the detail edits brief and status through the same dialog, and shows the server error', async () => {
    vi.stubGlobal(
      'fetch',
      fakeFetch({ ...api, '/api/tasks/alpha/t-fail': { ...detail, id: 't-fail' } }),
    )
    const user = userEvent.setup()
    renderAt('/p/alpha/t/alpha-1')
    const article = await screen.findByRole('article')
    await user.click(within(article).getByRole('button', { name: 'write brief' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    const area = within(dialog).getByRole('textbox')
    await user.type(area, 'line one{Enter}line two')
    expect(posts).toHaveLength(0)
    await user.keyboard('{Meta>}{Enter}{/Meta}')
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0].body).toEqual({ brief: 'line one\nline two' })

    await user.selectOptions(within(article).getByRole('combobox', { name: 'status' }), 'done')
    expect(within(dialog).getByText('Moves alpha-1 from todo to done.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'move to done' }))
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1].body).toEqual({ status: 'done' })
  })

  it('a rejected mutation keeps the dialog open with the error text', async () => {
    vi.stubGlobal(
      'fetch',
      fakeFetch({
        ...api,
        '/api/tasks/alpha/t-fail': { ...detail, id: 't-fail', brief: undefined },
      }),
    )
    const user = userEvent.setup()
    renderAt('/p/alpha/t/t-fail')
    const article = await screen.findByRole('article')
    await user.selectOptions(within(article).getByRole('combobox', { name: 'status' }), 'done')
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    await user.click(within(dialog).getByRole('button', { name: 'move to done' }))
    expect(await within(dialog).findByText(/error: status "bogus"/)).toBeInTheDocument()
    expect(dialog).toHaveAttribute('open')
  })
})

describe('sidebar', () => {
  it('columns: glyph, name, four counters with zeros dimmed, repos under a multi-repo group', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/')
    const nav = await screen.findByRole('navigation', { name: 'Projects' })
    const rows = within(nav).getAllByRole('row')
    // header + alpha + acme + 2 repos of acme
    expect(rows).toHaveLength(5)
    expect(within(rows[1]).getByLabelText('worst: crit')).toHaveTextContent('✗')
    expect(within(rows[1]).getByRole('link', { name: 'ALPHA' })).toHaveAttribute('href', '/g/alpha')
    expect(within(rows[1]).getAllByText('·')).toHaveLength(2)
    expect(within(rows[2]).getByRole('link', { name: 'ACME' })).toHaveAttribute('href', '/g/acme')
    expect(within(rows[3]).getByRole('link', { name: '· acme-api' })).toHaveAttribute(
      'href',
      '/g/acme?tab=board&repo=acme-api',
    )
    expect(within(nav).getByText('Projects · worst first')).toBeInTheDocument()
  })

  it('plain drops the counters; rail drops names and repos, keeps the glyphs', async () => {
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/config': config({ variant: 'plain' }) }))
    const plain = renderAt('/')
    let nav = await screen.findByRole('navigation', { name: 'Projects' })
    await vi.waitFor(() => expect(within(nav).queryByRole('columnheader')).toBeNull())
    expect(within(nav).getByRole('link', { name: 'ALPHA' })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: '· acme-api' })).toBeInTheDocument()
    plain.unmount()
    vi.unstubAllGlobals()

    vi.stubGlobal(
      'fetch',
      fakeFetch({ ...api, '/api/config': config({ variant: 'rail', sort: 'last_activity' }) }),
    )
    renderAt('/')
    nav = await screen.findByRole('navigation', { name: 'Projects' })
    await vi.waitFor(() => expect(within(nav).queryByText('Projects · worst first')).toBeNull())
    expect(within(nav).queryByRole('link', { name: '· acme-api' })).toBeNull()
    expect(within(nav).getByLabelText('worst: crit')).toBeInTheDocument()
    // last_activity: acme (Jan 5) before alpha (Jan 2).
    const glyphs = within(nav)
      .getAllByRole('row')
      .map((r) => r.getAttribute('data-severity'))
    expect(glyphs).toEqual(['warn', 'crit'])
  })
})

describe('keyboard', () => {
  it('j/k select rows, Enter opens, Esc closes; j is ignored while typing', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/p/alpha')
    await screen.findByText('Second')

    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    await user.keyboard('j')
    expect(input).toHaveValue('j')
    expect(screen.queryByRole('listitem', { current: true })).toBeNull()
    input.blur()
    input.remove()

    await user.keyboard('j')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('First')
    await user.keyboard('j')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('Second')
    await user.keyboard('k')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('First')

    await user.keyboard('{Enter}')
    expect(await screen.findByRole('article')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await vi.waitFor(() => expect(screen.queryByRole('article')).toBeNull())
  })

  it('? opens the shortcut list', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/')
    await screen.findByRole('navigation', { name: 'Projects' })
    await user.keyboard('?')
    const dialog = screen.getByRole('dialog', { name: 'Keyboard shortcuts' })
    expect(dialog).toHaveAttribute('open')
    expect(within(dialog).getByText('next row')).toBeInTheDocument()
    expect(within(dialog).getByText('Today (home)')).toBeInTheDocument()
  })
})

describe('palette', () => {
  it('opens on cmd+k, filters by title, and navigates on select', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/p/alpha')
    await screen.findByText('Second')

    // A closed <dialog> is display:none, and an accessible name is not
    // computed for a hidden element - so the closed state is asserted on the
    // node itself and the role query runs once it is open.
    const closed = document.querySelector('dialog[aria-label="Command palette"]')
    expect(closed).not.toHaveAttribute('open')
    await user.keyboard('{Meta>}k{/Meta}')
    const dialog = screen.getByRole('dialog', { name: 'Command palette' })
    expect(dialog).toHaveAttribute('open')
    // The field has the focus at once: what is typed next filters (ux-audit F-03).
    expect(within(dialog).getByRole('combobox')).toHaveFocus()
    expect(within(dialog).getByText('go to project Alpha')).toBeInTheDocument()
    expect(within(dialog).getByText('runs')).toBeInTheDocument()

    await user.type(within(dialog).getByRole('combobox'), 'sec')
    expect(within(dialog).getByText('open task alpha-2 Second')).toBeInTheDocument()
    expect(within(dialog).queryByText('open task alpha-1 First')).toBeNull()
    expect(within(dialog).queryByText('go to project Alpha')).toBeNull()

    await user.click(within(dialog).getByText('open task alpha-2 Second'))
    expect(dialog).not.toHaveAttribute('open')
    expect(
      await screen.findByText(/no such endpoint: \/api\/tasks\/alpha\/alpha-2/),
    ).toBeInTheDocument()
  })
})

describe('runs', () => {
  it('defaults to unfinished only, needs-me first, with the three times; remote rows only after an explicit fetch', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/runs')
    const table = await screen.findByRole('table', { name: 'Runs' })
    // alpha-1 is in needs_me (crit) -> first; alpha-9 is live -> kept; acme-api-9 finished -> hidden.
    let trackers = () =>
      within(screen.getByRole('table', { name: 'Runs' }))
        .getAllByRole('row')
        .slice(1)
        .map((r) => within(r).getAllByRole('cell')[2].textContent)
    expect(trackers()).toEqual(['alpha-1', 'alpha-9'])
    const live = within(table).getAllByRole('row')[2]
    const cells = within(live)
      .getAllByRole('cell')
      .map((c) => c.textContent)
    expect(cells[0]).toBe('▶')
    expect(cells[4]).toBe('running 1/3')
    expect(cells[6]).toMatch(/\d+[dh]$/) // duration up to now
    expect(cells[7]).toBe('') // no end while live
    expect(cells[8]).toMatch(/\d+[dh]$/) // heartbeat age
    const failed = within(table).getAllByRole('row')[1]
    const fcells = within(failed)
      .getAllByRole('cell')
      .map((c) => c.textContent)
    expect(fcells[0]).toBe('✗')
    expect(fcells[6]).toBe('2h')
    expect(fcells[8]).toBe('')
    expect(screen.getByText(/remote: not fetched/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'unfinished only' }))
    expect(trackers()).toEqual(['alpha-1', 'alpha-9', 'acme-api-9'])
    await user.click(screen.getByRole('button', { name: 'newest' }))
    expect(trackers()).toEqual(['alpha-9', 'alpha-1', 'acme-api-9'])
    await user.click(screen.getByRole('button', { name: 'by project' }))
    expect(trackers()).toEqual(['alpha-9', 'alpha-1', 'acme-api-9'])

    await user.click(screen.getByRole('button', { name: 'fetch remote' }))
    expect(await within(table).findByText('vps/beta')).toBeInTheDocument()
    expect(within(table).getAllByText('done 2/2')).toHaveLength(2)
    expect(within(table).getAllByRole('row')).toHaveLength(5)
    expect(screen.getByText(/remote: fetched just now/)).toBeInTheDocument()
  })
})

describe('group page', () => {
  it('shows the header, repo chips, tabs; the overview has notes, the narrowed queue, doing by activity and trackers', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/g/acme')
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('ACME')
    expect(screen.getByText(/group · 2 repos/)).toBeInTheDocument()
    const repos = screen.getByRole('list', { name: 'Repos' })
    expect(within(repos).getByRole('link', { name: 'acme-api' })).toHaveAttribute(
      'href',
      '/g/acme?tab=board&repo=acme-api',
    )
    expect(within(repos).getByText(/· Livingdocs/)).toBeInTheDocument()
    const tabs = screen.getByRole('tablist')
    expect(
      within(tabs)
        .getAllByRole('tab')
        .map((t) => t.textContent),
    ).toEqual(['overview', 'board', 'runs', 'changes'])
    expect(within(tabs).getByRole('tab', { selected: true })).toHaveTextContent('overview')

    const left = screen.getByRole('region', { name: 'Where we left off' })
    expect(within(left).getByText('ACME-60 in two repos')).toBeInTheDocument()
    expect(within(left).getByRole('button', { name: 'add notes' })).toBeInTheDocument()
    expect(within(left).getByRole('button', { name: 'edit notes' })).toBeInTheDocument()

    // The queue, narrowed to acme by the API (?group=acme route).
    const waiting = screen.getByRole('region', { name: 'Waiting on' })
    expect(within(waiting).getByText('Acme-api thing')).toBeInTheDocument()
    expect(within(waiting).queryByText('Second')).toBeNull()
    expect(
      within(screen.getByRole('region', { name: 'Needs me' })).getByText('Nothing needs you.'),
    ).toBeInTheDocument()

    // Doing: acme-zap's fresher task first, acme-api's idle one marked quiet.
    const doing = await screen.findByRole('region', { name: 'In progress (doing)' })
    await vi.waitFor(() => expect(within(doing).getAllByRole('listitem')).toHaveLength(2))
    const items = within(doing).getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('acme-zap-1')
    expect(items[0]).toHaveTextContent('tracker 1 done · 1 doing of 2')
    expect(items[1]).toHaveTextContent('acme-api-2')
    expect(items[1]).toHaveAttribute('data-idle', 'true')
    expect(within(items[1]).getByLabelText('quiet')).toBeInTheDocument()

    // Trackers joined with the runs rows.
    const trackers = screen.getByRole('table', { name: 'Trackers' })
    const rows = within(trackers).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('acme-api-9')
    expect(rows[0]).toHaveTextContent('done 2/2')
    expect(rows[1]).toHaveTextContent('acme-zap-1')
    expect(rows[1]).toHaveTextContent('1 done · 1 doing')
  })

  it('/p/<slug> redirects to the group board on that repo; the board switches repos; [ ] step the tabs', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/p/acme-zap')
    const tabs = await screen.findByRole('tablist')
    expect(within(tabs).getByRole('tab', { selected: true })).toHaveTextContent('board')
    const repo = screen.getByRole('navigation', { name: 'Repo' })
    expect(within(repo).getByRole('link', { current: 'page' })).toHaveTextContent('acme-zap')
    expect(await screen.findByRole('region', { name: 'doing' })).toHaveTextContent('Zap task')

    await user.click(within(repo).getByRole('link', { name: 'acme-api' }))
    expect(await screen.findByText('Acme-api thing')).toBeInTheDocument()

    await user.keyboard(']')
    await vi.waitFor(() =>
      expect(
        within(screen.getByRole('tablist')).getByRole('tab', { selected: true }),
      ).toHaveTextContent('runs'),
    )
    // Nothing of the group is unfinished: the table only appears on "all runs".
    expect(await screen.findByText('no runs')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'unfinished only' }))
    const table = await screen.findByRole('table', { name: 'Runs' })
    expect(within(table).getAllByRole('row')).toHaveLength(2)
    expect(within(table).getByText('acme-api-9')).toBeInTheDocument()
    expect(within(table).queryByText('alpha-9')).toBeNull()
    // user-event reads `[` as a descriptor bracket; `[[` is the literal key.
    await user.keyboard('[[')
    await user.keyboard('[[')
    await vi.waitFor(() =>
      expect(
        within(screen.getByRole('tablist')).getByRole('tab', { selected: true }),
      ).toHaveTextContent('overview'),
    )
  })

  it('the changes tab lists the group events only', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/g/acme?tab=changes')
    const list = await screen.findByRole('region', { name: 'Changes' })
    await vi.waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(1))
    expect(list).toHaveTextContent('Acme-api thing')
    expect(list).toHaveTextContent('moved')
    expect(list).not.toHaveTextContent('alpha event')
  })
})

describe('changes screen', () => {
  it('lists every event with source chips and states; a source chip and a group chip narrow the list', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/changes')
    const feed = await screen.findByRole('region', { name: 'Feed' })
    expect(await within(feed).findAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Changes')
    // The source with an error says so; the disabled one is marked off.
    const sources = screen.getByRole('group', { name: 'Sources' })
    expect(within(sources).getByRole('button', { name: /^git/ })).toHaveAttribute(
      'data-state',
      'error',
    )
    expect(within(sources).getByRole('button', { name: /^slack/ })).toHaveAttribute(
      'data-state',
      'off',
    )
    expect(within(sources).getByText('git: error: gh: not logged in')).toBeInTheDocument()
    // The report panel holds its place, off.
    expect(screen.getByRole('region', { name: 'Report' })).toHaveTextContent('report is off')
    // Toggle git off: only the pm event stays.
    await user.click(within(sources).getByRole('button', { name: /^git/ }))
    expect(within(feed).getAllByRole('listitem')).toHaveLength(1)
    expect(within(feed).getByText('Acme-api thing')).toBeInTheDocument()
    await user.click(within(sources).getByRole('button', { name: /^git/ }))
    expect(within(feed).getAllByRole('listitem')).toHaveLength(2)
    // The group chip narrows via the URL.
    // The phone bar carries the same chips (jsdom hides nothing); the page's are the last.
    const chips = screen.getAllByRole('navigation', { name: 'Group filter' }).at(-1)!
    await user.click(within(chips).getByRole('link', { name: /ALPHA/ }))
    await vi.waitFor(() => expect(within(feed).getAllByRole('listitem')).toHaveLength(1))
    expect(within(feed).getByText('alpha event')).toBeInTheDocument()
  })

  it('mark all seen asks first, then posts the seen mark with the client header; a row seen carries its stamp', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/changes')
    const feed = await screen.findByRole('region', { name: 'Feed' })
    await within(feed).findAllByRole('listitem')
    await user.click(screen.getByRole('button', { name: /mark all seen/ }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    expect(posts).toHaveLength(0)
    await user.click(within(dialog).getByRole('button', { name: 'mark seen' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({ path: '/api/changes/seen', header: 'cockpit', body: undefined })
    await vi.waitFor(() => expect(dialog).not.toHaveAttribute('open'))
    await user.click(
      within(within(feed).getAllByRole('listitem')[1]).getByRole('button', { name: 'seen' }),
    )
    await user.click(within(dialog).getByRole('button', { name: 'mark seen' }))
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1].body).toEqual({ ts: '2026-01-02T09:10:00Z' })
  })

  it('dismiss all posts the seen mark with no dialog; dismissed rows hide until shown', async () => {
    const oneSeen = {
      ...changes,
      events: [changes.events[0], { ...changes.events[1], seen: true }],
      unseen: 1,
    }
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/changes': oneSeen }))
    const user = userEvent.setup()
    renderAt('/changes')
    const feed = await screen.findByRole('region', { name: 'Feed' })
    expect(await within(feed).findAllByRole('listitem')).toHaveLength(1)
    expect(within(feed).queryByText('alpha event')).toBeNull()
    await user.click(within(feed).getByRole('button', { name: '1 dismissed · show' }))
    expect(within(feed).getAllByRole('listitem')).toHaveLength(2)
    await user.click(within(feed).getByRole('button', { name: 'hide dismissed' }))
    expect(within(feed).getAllByRole('listitem')).toHaveLength(1)
    await user.click(within(feed).getByRole('button', { name: 'dismiss all (1)' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({ path: '/api/changes/seen', header: 'cockpit', body: undefined })
  })
})

describe('settings screen', () => {
  const settingsApi = { ...api, '/api/config': settingsConfig }
  it('renders every group with the API values; Save is disabled until something changes and then posts only the diff', async () => {
    vi.stubGlobal('fetch', fakeFetch(settingsApi))
    const user = userEvent.setup()
    renderAt('/settings')
    const form = await screen.findByRole('form', { name: 'Cockpit settings' })
    expect(within(form).getByRole('checkbox', { name: 'pm' })).toBeChecked()
    expect(within(form).getByRole('checkbox', { name: 'github (gh)' })).not.toBeChecked()
    expect(within(form).getByRole('spinbutton', { name: 'cutoff hour' })).toHaveValue(18)
    expect(within(form).getByRole('spinbutton', { name: 'refresh every minutes' })).toHaveValue(30)
    expect(within(form).getByRole('textbox', { name: 'refresh window' })).toHaveValue('07:00-20:00')
    expect(within(form).getByRole('spinbutton', { name: 'doing idle' })).toHaveValue(7)
    expect(within(form).getByRole('checkbox', { name: 'Needs me' })).toBeChecked()
    expect(within(form).getByRole('checkbox', { name: 'Recently touched' })).not.toBeChecked()
    expect(within(form).getByRole('combobox', { name: 'sidebar variant' })).toHaveValue('columns')
    // Groups: the configured ACME with its name and order, alpha unconfigured, members listed.
    expect(within(form).getByRole('textbox', { name: 'name of acme' })).toHaveValue('ACME')
    expect(within(form).getByRole('spinbutton', { name: 'order of acme' })).toHaveValue(1)
    expect(within(form).getByRole('textbox', { name: 'name of alpha' })).toHaveValue('')
    expect(within(form).getByRole('combobox', { name: 'group of acme-api' })).toHaveValue('acme')
    const save = within(form).getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()

    await user.click(within(form).getByRole('checkbox', { name: 'Recently touched' }))
    await user.selectOptions(
      within(form).getByRole('combobox', { name: 'sidebar variant' }),
      'rail',
    )
    const hour = within(form).getByRole('spinbutton', { name: 'cutoff hour' })
    await user.clear(hour)
    await user.type(hour, '20')
    await user.clear(within(form).getByRole('textbox', { name: 'name of alpha' }))
    await user.type(within(form).getByRole('textbox', { name: 'name of alpha' }), 'Alpha!')
    expect(save).toBeEnabled()
    await user.click(save)
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/settings',
      header: 'cockpit',
      body: {
        cutoff_hour: 20,
        sections: { recent: true },
        sidebar: { variant: 'rail' },
        groups: [{ slug: 'alpha', name: 'Alpha!' }],
      },
    })
    expect(await screen.findByRole('status')).toHaveTextContent('saved')
  })

  it('sleeping a project needs the dialog; a Slack row saves to its project', async () => {
    vi.stubGlobal('fetch', fakeFetch(settingsApi))
    const user = userEvent.setup()
    renderAt('/settings')
    const asleep = await screen.findByRole('region', { name: 'Asleep projects' })
    expect(asleep).toHaveTextContent('none asleep')
    await user.click(within(asleep).getByText('put a project to sleep…'))
    await user.click(within(asleep).getAllByRole('button', { name: 'sleep' })[1])
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/leaves every group/)
    expect(posts).toHaveLength(0)
    await user.click(within(dialog).getByRole('button', { name: 'sleep' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/projects/acme-api',
      header: 'cockpit',
      body: { archived: true },
    })

    const slack = screen.getByRole('region', { name: 'Slack per project' })
    const channels = within(slack).getByRole('textbox', { name: 'slack channels of alpha' })
    await user.type(channels, '#dev, product')
    await user.type(within(slack).getByRole('textbox', { name: 'slack workspace of alpha' }), 'atlas')
    await user.click(within(channels.closest('tr')!).getByRole('button', { name: 'save' }))
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1]).toEqual({
      path: '/api/projects/alpha',
      header: 'cockpit',
      body: { slack: { workspace: 'atlas', channels: ['#dev', 'product'] } },
    })
  })

  it('moving a repo between groups goes through the dialog and writes group: to the repo', async () => {
    vi.stubGlobal('fetch', fakeFetch(settingsApi))
    const user = userEvent.setup()
    renderAt('/settings')
    const form = await screen.findByRole('form', { name: 'Cockpit settings' })
    await user.selectOptions(within(form).getByRole('combobox', { name: 'group of alpha' }), 'acme')
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(within(dialog).getByText(/Moves alpha into group acme/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'move to acme' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/projects/alpha',
      header: 'cockpit',
      body: { group: 'acme' },
    })
  })
})

describe('run control goes through the dialog with the argv preview', () => {
  const runsApi = { ...api, '/api/attention': attentionRuns }
  it('resume run shows the server plan, a flag re-plans, Esc sends nothing, confirm posts the flags', async () => {
    vi.stubGlobal('fetch', fakeFetch(runsApi))
    const user = userEvent.setup()
    renderAt('/')
    const needs = await screen.findByRole('region', { name: 'Needs me' })
    const epic = within(needs).getAllByRole('listitem')[1]
    expect(within(epic).getByRole('button', { name: 'kill' })).toBeEnabled()
    await user.click(within(epic).getByRole('button', { name: 'resume run' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    const preview = await within(dialog).findByLabelText('command preview')
    await vi.waitFor(() =>
      expect(preview).toHaveTextContent('cd /repo/alpha && pm run-epic alpha alpha-9'),
    )
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/already in flight/)
    // The additional flag is offered but disabled: no slots on this project.
    expect(within(dialog).getByRole('checkbox', { name: /additional worktree/ })).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox', { name: /--yolo/ }))
    await vi.waitFor(() => expect(preview).toHaveTextContent('--yolo'))
    expect(posts).toHaveLength(0)
    await user.keyboard('{Escape}')
    await vi.waitFor(() => expect(dialog).not.toHaveAttribute('open'))
    expect(posts).toHaveLength(0)

    await user.click(within(epic).getByRole('button', { name: 'resume run' }))
    await within(dialog).findByLabelText('command preview')
    await user.click(within(dialog).getByRole('checkbox', { name: /--yolo/ }))
    await vi.waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'start run' })).toBeEnabled(),
    )
    await user.click(within(dialog).getByRole('button', { name: 'start run' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/runs/alpha/alpha-9/resume_run',
      header: 'cockpit',
      body: { yolo: true },
    })
    expect(await screen.findByRole('status')).toHaveTextContent('started run-epic (pid 4242)')
  })

  it('kill names the pid it would signal and posts the kill', async () => {
    vi.stubGlobal('fetch', fakeFetch(runsApi))
    const user = userEvent.setup()
    renderAt('/')
    const needs = await screen.findByRole('region', { name: 'Needs me' })
    const epic = within(needs).getAllByRole('listitem')[1]
    await user.click(within(epic).getByRole('button', { name: 'kill' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    await vi.waitFor(() =>
      expect(within(dialog).getByLabelText('command preview')).toHaveTextContent(
        'run-epic pid 777',
      ),
    )
    expect(within(dialog).getByText(/Sends SIGTERM to run-epic pid 777/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'kill' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0].path).toBe('/api/runs/alpha/alpha-9/kill')
    expect(posts[0].header).toBe('cockpit')
  })
})

describe('report panel', () => {
  it('off says so and offers no button', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    renderAt('/changes')
    const panel = await screen.findByRole('region', { name: 'Report' })
    await vi.waitFor(() => expect(panel).toHaveAttribute('data-state', 'off'))
    expect(panel).toHaveTextContent('report is off')
    expect(within(panel).queryByRole('button', { name: /write/ })).toBeNull()
  })

  it('done shows the prose, the cost and the open suggestions', async () => {
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/report': reportDone }))
    renderAt('/changes')
    const done = await screen.findByRole('region', { name: 'Report' })
    await vi.waitFor(() => expect(done).toHaveAttribute('data-state', 'done'))
    expect(done).toHaveTextContent('**ACME**: acme-api-1 waits on review.')
    expect(done).toHaveTextContent('haiku · 4.4k tokens · 9 s')
    expect(within(done).getByRole('button', { name: 'write again' })).toBeInTheDocument()
    const items = within(done).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(within(items[0]).getByRole('button', { name: 'do' })).toBeEnabled()
    expect(within(items[1]).getByRole('button', { name: 'do' })).toBeDisabled()
  })

  it('"do" opens the mapped action\'s dialog on the task; dismiss posts the id; "write again" posts with no dialog', async () => {
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/report': reportDone }))
    const user = userEvent.setup()
    renderAt('/changes')
    const panel = await screen.findByRole('region', { name: 'Report' })
    await vi.waitFor(() => expect(panel).toHaveAttribute('data-state', 'done'))
    const items = within(panel).getAllByRole('listitem')
    await user.click(within(items[0]).getByRole('button', { name: 'do' }))
    const dialog = screen.getByRole('dialog', { name: 'Confirm' })
    expect(dialog).toHaveAttribute('open')
    expect(within(dialog).getByText(/Moves acme-api-1 back to todo/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'back to todo' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/tasks/acme-api/acme-api-1',
      header: 'cockpit',
      body: { status: 'todo', waiting_for: '' },
    })
    await user.click(within(items[1]).getByRole('button', { name: 'dismiss' }))
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1]).toEqual({
      path: '/api/report/dismiss',
      header: 'cockpit',
      body: { id: 'sg2' },
    })
    // "write again" posts straight away - the panel already says it costs tokens.
    await user.click(within(panel).getByRole('button', { name: 'write again' }))
    await vi.waitFor(() => expect(posts).toHaveLength(3))
    expect(posts[2]).toEqual({ path: '/api/report', header: 'cockpit', body: undefined })
  })
})

describe('dismiss on home', () => {
  it('dismisses a row with no dialog, bulk-dismisses the old ones, restores the hidden', async () => {
    const needs = {
      name: 'needs_me',
      total: 2,
      dismissed: 3,
      rows: [
        row('needs_me', 'alpha', 'alpha-1', 'Old one', {
          actions: ['open', 'dismiss'],
          age_seconds: 20 * 86400,
          since: '2026-01-01T00:00:00Z',
        }),
        row('needs_me', 'alpha', 'alpha-2', 'Fresh one', {
          actions: ['open', 'dismiss'],
          age_seconds: 3600,
          since: '2026-01-02T00:00:00Z',
        }),
      ],
    }
    vi.stubGlobal(
      'fetch',
      fakeFetch({ ...api, '/api/attention': { ...attention, sections: [needs] } }),
    )
    const user = userEvent.setup()
    renderAt('/')
    const sec = await screen.findByRole('region', { name: 'Needs me' })
    const items = within(sec).getAllByRole('listitem')
    await user.click(within(items[1]).getByRole('button', { name: 'dismiss' }))
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/attention/dismiss',
      header: 'cockpit',
      body: {
        rows: [
          {
            section: 'needs_me',
            project: 'alpha',
            task_id: 'alpha-2',
            since: '2026-01-02T00:00:00Z',
          },
        ],
      },
    })
    await user.click(within(sec).getByRole('button', { name: 'dismiss older than 14d (1)' }))
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1].body).toEqual({
      rows: [
        {
          section: 'needs_me',
          project: 'alpha',
          task_id: 'alpha-1',
          since: '2026-01-01T00:00:00Z',
        },
      ],
    })
    await user.click(within(sec).getByRole('button', { name: '3 hidden · restore' }))
    await vi.waitFor(() => expect(posts).toHaveLength(3))
    expect(posts[2]).toEqual({
      path: '/api/attention/restore',
      header: 'cockpit',
      body: { section: 'needs_me' },
    })
  })
})

describe('review screen', () => {
  const reviewDone = {
    id: 'org-app-7-1',
    url: 'https://github.com/org/app/pull/7',
    repo: 'org/app',
    number: 7,
    project: 'alpha',
    dir: '/repos/app',
    config_dir: '/home/.claude',
    pid: 1,
    started: '2026-01-02T09:00:00Z',
    finished: '2026-01-02T09:08:00Z',
    state: 'done',
  }
  it('starts a review from a pasted PR URL and lists the reviews', async () => {
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/reviews': { reviews: [reviewDone] } }))
    const user = userEvent.setup()
    renderAt('/review')
    const table = await screen.findByRole('table', { name: 'Reviews' })
    expect(within(table).getByRole('link', { name: 'org/app#7' })).toHaveAttribute(
      'href',
      '/review/org-app-7-1',
    )
    expect(within(table).getByText('8m')).toBeInTheDocument()
    const input = screen.getByRole('textbox', { name: 'PR URL' })
    expect(screen.getByRole('button', { name: /^review/ })).toBeDisabled()
    await user.type(input, 'https://github.com/OrbitOrg/app.orbit/pull/1003/changes')
    await user.click(
      screen.getByRole('button', { name: 'review OrbitOrg/app.orbit#1003' }),
    )
    await vi.waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/reviews',
      header: 'cockpit',
      body: { url: 'https://github.com/OrbitOrg/app.orbit/pull/1003/changes' },
    })
  })

  it('a review page renders the report', async () => {
    vi.stubGlobal(
      'fetch',
      fakeFetch({
        ...api,
        '/api/reviews/org-app-7-1': {
          ...reviewDone,
          report: '### Code review - PR #7\n\nNo issues found.',
        },
      }),
    )
    renderAt('/review/org-app-7-1')
    expect(await screen.findByRole('heading', { name: 'Code review - PR #7' })).toBeInTheDocument()
    expect(screen.getByText('No issues found.')).toBeInTheDocument()
  })
})

describe('solo report rows', () => {
  it('the title opens the shift report', async () => {
    const solo = {
      name: 'solo_reports',
      total: 1,
      rows: [
        {
          section: 'solo_reports',
          severity: 'info',
          project: 'alpha',
          group: 'alpha',
          shift: 'abc',
          title: 'alpha-1 · First',
          reason: 'solo shift closed',
          age_seconds: 3600,
          actions: ['open_report', 'dismiss'],
        },
      ],
    }
    vi.stubGlobal(
      'fetch',
      fakeFetch({ ...api, '/api/attention': { ...attention, sections: [solo] } }),
    )
    renderAt('/')
    const sec = await screen.findByRole('region', { name: 'Solo reports' })
    expect(within(sec).getByRole('link', { name: 'alpha-1 · First' })).toHaveAttribute(
      'href',
      '/solo/alpha/abc',
    )
  })
})

describe('home rows the API cannot open', () => {
  const dup = {
    ...attention,
    sections: [
      {
        name: 'changes',
        rows: [
          row('changes', 'alpha', 'alpha-2', 'ev1 on alpha-2', {
            actions: ['mark_seen', 'open'],
            since: '2026-01-02T09:00:00Z',
          }),
          row('changes', 'alpha', 'alpha-2', 'ev2 on alpha-2', {
            actions: ['mark_seen', 'open'],
            since: '2026-01-02T09:30:00Z',
          }),
          row('changes', '', undefined as unknown as string, 'slack dm one', {
            actions: ['mark_seen'],
            group: '',
          }),
          row('changes', '', undefined as unknown as string, 'slack dm two', {
            actions: ['mark_seen'],
            group: '',
          }),
          row('changes', 'alpha', 'alpha-1', 'ev on alpha-1', { actions: ['mark_seen', 'open'] }),
        ],
        total: 5,
      },
    ],
  }
  it('j walks past two events on one task and two project-less rows; a row without open is no link and Enter does nothing', async () => {
    vi.stubGlobal('fetch', fakeFetch({ ...api, '/api/attention': dup }))
    const user = userEvent.setup()
    renderAt('/')
    await screen.findByText('ev on alpha-1')
    const titles = [
      'ev1 on alpha-2',
      'ev2 on alpha-2',
      'slack dm one',
      'slack dm two',
      'ev on alpha-1',
    ]
    for (const title of titles) {
      await user.keyboard('j')
      const current = screen.getAllByRole('listitem', { current: true })
      expect(current).toHaveLength(1)
      expect(current[0]).toHaveTextContent(title)
    }
    // a project-less row: no link, and Enter on it navigates nowhere
    expect(screen.getByText('slack dm one').closest('a')).toBeNull()
    expect(screen.getByText('ev1 on alpha-2').closest('a')).not.toBeNull()
    await user.keyboard('k')
    await user.keyboard('k')
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('slack dm one')
    await user.keyboard('{Enter}')
    expect(screen.queryByRole('article')).toBeNull()
    expect(screen.getByRole('listitem', { current: true })).toHaveTextContent('slack dm one')
  })
})
