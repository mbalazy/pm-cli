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

function fakeFetch(routes: Record<string, unknown>) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const [path, qs] = url.split('?')
    const key = qs?.includes('remote=1') ? `${path}?remote=1` : path
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

const api = {
  '/api/projects': projects,
  '/api/tasks': tasks,
  '/api/tasks/alpha/alpha-1': detail,
  '/api/runs': runs,
  '/api/runs?remote=1': remoteRuns,
}

afterEach(() => vi.unstubAllGlobals())

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
    expect(within(dialog).getByText('next task')).toBeInTheDocument()
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
    const table = await screen.findByRole('table')
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
