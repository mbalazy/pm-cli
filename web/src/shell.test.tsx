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
      task_counts: { todo: 2 },
      statuses: ['todo', 'done'],
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
      run: { state: 'running', done: 1, total: 3 },
      acceptance: {},
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
  },
})
const changes = {
  cutoff: '2026-01-01T18:00:00Z',
  events: [],
  unseen: 0,
  sources: [{ name: 'pm', enabled: true, events: 1, last_fetch: '2026-01-02T09:30:00Z' }],
}

const focusPlan = { date: '2026-01-02', task_ids: [] as string[], tasks: [] as unknown[] }

const api = {
  '/api/focus': focusPlan,
  '/api/projects': projects,
  '/api/tasks': tasks,
  '/api/tasks/alpha/alpha-1': detail,
  '/api/runs': runs,
  '/api/runs?remote=1': remoteRuns,
  '/api/attention': attention,
  '/api/attention?group=acme': attentionNzz,
  '/api/config': config({}),
  '/api/changes': changes,
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
    await user.click(within(chips).getByRole('link', { name: /ACME/ }))
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
    expect(within(rows[1]).getByRole('link', { name: 'ALPHA' })).toHaveAttribute('href', '/p/alpha')
    expect(within(rows[1]).getAllByText('·')).toHaveLength(2)
    expect(within(rows[2]).getByRole('link', { name: 'ACME' })).toHaveAttribute('href', '/p/acme-api')
    expect(within(rows[3]).getByRole('link', { name: '· acme-api' })).toBeInTheDocument()
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
  it('lists local rows and appends remote rows only after an explicit fetch', async () => {
    vi.stubGlobal('fetch', fakeFetch(api))
    const user = userEvent.setup()
    renderAt('/runs')
    const table = await screen.findByRole('table', { name: 'Runs' })
    expect(within(table).getByText('running 1/3')).toBeInTheDocument()
    expect(within(table).queryByText('vps/beta')).toBeNull()
    expect(screen.getByText(/remote: not fetched/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'fetch remote' }))
    expect(await within(table).findByText('vps/beta')).toBeInTheDocument()
    expect(within(table).getByText('done 2/2')).toBeInTheDocument()
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(screen.getByText(/remote: fetched just now/)).toBeInTheDocument()
  })
})
