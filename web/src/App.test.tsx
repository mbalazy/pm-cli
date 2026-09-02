import { QueryClient } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { createMemoryHistory } from '@tanstack/react-router'

import { App } from './App'

// Smoke test: the real router, the real hooks, a fake /api. Proves the pipe
// from URL to rendered list; the grouping rules themselves are lib/ tests.

const projects = {
  projects: [
    {
      slug: 'alpha',
      name: 'Alpha',
      task_counts: { todo: 1, doing: 1, archived: 5 },
      statuses: ['todo', 'doing', 'done'],
      landing_statuses: ['merged', 'pushed'],
    },
    {
      slug: 'beta',
      name: 'Beta',
      task_counts: {},
      statuses: ['todo', 'done'],
      landing_statuses: [],
    },
  ],
}

const tasks = {
  tasks: [
    {
      id: 'alpha-2',
      title: 'Second',
      status: 'doing',
      project: 'alpha',
      updated: '2026-01-02',
      session_count: 0,
      tags: ['ui'],
    },
    {
      id: 'alpha-1',
      title: 'First',
      status: 'todo',
      project: 'alpha',
      updated: '2026-01-01',
      session_count: 0,
    },
  ],
  total: 2,
  shown: 2,
}

function fakeFetch(routes: Record<string, unknown>) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = url.split('?')[0]
    const body = routes[path]
    if (body === undefined) {
      return new Response(JSON.stringify({ error: `no such endpoint: ${path}` }), { status: 404 })
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

afterEach(() => vi.unstubAllGlobals())

describe('App', () => {
  it('renders the sidebar and the tasks of the project in the URL, grouped by status', async () => {
    vi.stubGlobal('fetch', fakeFetch({ '/api/projects': projects, '/api/tasks': tasks }))
    renderAt('/p/alpha')

    const nav = await screen.findByRole('navigation', { name: 'Projects' })
    expect(within(nav).getByText('Alpha')).toBeInTheDocument()
    expect(within(nav).getByText('(2)')).toBeInTheDocument()
    expect(within(nav).getByRole('link', { current: 'page' })).toHaveTextContent('Alpha')

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('Alpha')
    const todo = screen.getByRole('region', { name: 'todo' })
    expect(within(todo).getByText('First')).toBeInTheDocument()
    const doing = screen.getByRole('region', { name: 'doing' })
    expect(within(doing).getByText('Second')).toBeInTheDocument()
    expect(within(doing).getByText('ui')).toBeInTheDocument()
    expect(
      within(screen.getByRole('region', { name: 'done' })).getByText('no tasks'),
    ).toBeInTheDocument()
  })

  it('shows the API error as text instead of an empty page', async () => {
    vi.stubGlobal('fetch', fakeFetch({ '/api/projects': projects }))
    renderAt('/p/alpha')
    expect(await screen.findByText(/no such endpoint: \/api\/tasks/)).toBeInTheDocument()
  })
})
