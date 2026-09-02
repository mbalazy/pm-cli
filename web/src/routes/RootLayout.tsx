import { Outlet, useLocation, useNavigate, useParams } from '@tanstack/react-router'
import { useState } from 'react'

import { useProjects, useTasks } from '../api/queries'
import { useLiveInvalidation } from '../api/useLiveInvalidation'
import { HelpDialog } from '../components/HelpDialog'
import { Palette } from '../components/Palette'
import { ProjectSidebar } from '../components/ProjectSidebar'
import { useRecentTasks } from '../hooks/useRecentTasks'
import { useShortcuts } from '../hooks/useShortcuts'
import { openTaskCount } from '../lib/groupByStatus'
import { paletteItems, type PaletteItem } from '../lib/paletteItems'

// The shell: sidebar (a drawer under 768 px), the page, the palette, the
// help dialog and the live feed. Composition only - hooks in, components out.

export function RootLayout() {
  const { slug } = useParams({ strict: false })
  const navigate = useNavigate()
  const projects = useProjects()
  const tasks = useTasks(slug ?? '')
  const { recent } = useRecentTasks()
  useLiveInvalidation()

  // The phone drawer is open for ONE path: any navigation - a task row, the
  // palette, the back button - closes it, without an effect to do so.
  const { pathname } = useLocation()
  const [drawerPath, setDrawerPath] = useState<string | null>(null)
  const drawerOpen = drawerPath === pathname
  const setDrawerOpen = (open: boolean) => setDrawerPath(open ? pathname : null)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [query, setQuery] = useState('')

  useShortcuts({
    palette: () => setPaletteOpen((o) => !o),
    help: () => setHelpOpen(true),
  })

  const items = paletteItems({
    projects: projects.data?.projects ?? [],
    tasks: tasks.data?.tasks ?? [],
    recent,
    query,
  })
  const choose = (item: PaletteItem) => {
    setPaletteOpen(false)
    setQuery('')
    void navigate({ to: item.to })
  }

  return (
    <div className="min-h-screen md:grid md:grid-cols-[14rem_1fr]">
      <div className="border-b p-2 md:hidden">
        <button type="button" className="underline" onClick={() => setDrawerOpen(!drawerOpen)}>
          {drawerOpen ? 'Hide projects' : 'Projects'}
        </button>
        <button type="button" className="ml-4 underline" onClick={() => setPaletteOpen(true)}>
          ⌘K
        </button>
      </div>
      <aside className={`border-r ${drawerOpen ? '' : 'hidden'} md:block`}>
        {projects.isPending && <p className="p-3 text-sm">loading…</p>}
        {projects.isError && (
          <p className="p-3 text-sm text-red-700">error: {projects.error.message}</p>
        )}
        {projects.data && (
          <ProjectSidebar
            activeSlug={slug}
            onNavigate={() => setDrawerOpen(false)}
            items={projects.data.projects
              .filter((p) => !p.archived)
              .map((p) => ({
                slug: p.slug,
                name: p.name,
                openTasks: openTaskCount(p.task_counts),
              }))}
          />
        )}
        <p className="hidden p-3 text-xs text-gray-400 md:block">
          <kbd>?</kbd> shortcuts · <kbd>⌘K</kbd> palette
        </p>
      </aside>
      <main className="min-w-0 p-4">
        <Outlet />
      </main>
      <Palette
        open={paletteOpen}
        query={query}
        items={items}
        onQueryChange={setQuery}
        onSelect={choose}
        onClose={() => setPaletteOpen(false)}
      />
      <HelpDialog open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  )
}
