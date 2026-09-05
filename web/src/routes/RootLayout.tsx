import { Link, Outlet, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { useState } from 'react'

import { useAttention, useConfig, useProjects, useTasks } from '../api/queries'
import { useLiveInvalidation } from '../api/useLiveInvalidation'
import { GroupChips } from '../components/GroupChips'
import { GroupSidebar } from '../components/GroupSidebar'
import { HelpDialog } from '../components/HelpDialog'
import { Palette } from '../components/Palette'
import { useRecentTasks } from '../hooks/useRecentTasks'
import { useShortcuts } from '../hooks/useShortcuts'
import { groupHotkeys, parseGroupFilter } from '../lib/groupFilter'
import { paletteItems, type PaletteItem } from '../lib/paletteItems'
import { SCREENS } from '../lib/screens'
import { groupMembers } from '../lib/groupView'
import { asleepProjects, sidebarLayout, sortGroups } from '../lib/sidebarView'

// The shell: the group sidebar (a chip strip on a phone), the page, the
// palette, the help dialog and the live feed. Composition only - hooks in,
// components out. The sidebar's shape is the server's config (/api/config).

export function RootLayout() {
  const { slug, group } = useParams({ strict: false })
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const projects = useProjects()
  // The palette searches the tasks of the repo on screen: the detail's, the
  // group board's (`?repo=`), else the group's first member.
  const repoOnScreen =
    slug ??
    (typeof search.repo === 'string' ? search.repo : undefined) ??
    (group ? groupMembers(projects.data?.projects, group)[0]?.slug : undefined) ??
    ''
  const tasks = useTasks(repoOnScreen)
  const attention = useAttention()
  const config = useConfig()
  const { recent } = useRecentTasks()
  useLiveInvalidation()

  const [paletteOpen, setPaletteOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [query, setQuery] = useState('')

  useShortcuts({
    palette: () => setPaletteOpen((o) => !o),
    help: () => setHelpOpen(true),
    today: () => void navigate({ to: '/' }),
    changes: () => void navigate({ to: '/changes' }),
    runs: () => void navigate({ to: '/runs' }),
    settings: () => void navigate({ to: '/settings' }),
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

  const layout = sidebarLayout(config.data?.cockpit.sidebar)
  const groups = sortGroups(
    attention.data?.groups ?? [],
    config.data?.cockpit.sidebar.sort,
    config.data?.cockpit.groups,
  )
  const asleep = asleepProjects(projects.data?.projects)
  const columns = layout.width ?? (layout.variant === 'rail' ? '3.5rem' : '15rem')

  return (
    <div
      className="min-h-screen md:grid"
      style={{ gridTemplateColumns: `${columns} minmax(0, 1fr)` }}
    >
      <div className="space-y-2 border-b p-2 md:hidden">
        <nav aria-label="Screens" className="flex flex-wrap gap-3 text-sm">
          {SCREENS.map((s) => (
            <Link
              key={s.to}
              to={s.to}
              className="underline"
              activeProps={{ className: 'font-bold' }}
            >
              {s.label}
            </Link>
          ))}
          <button type="button" className="ml-auto underline" onClick={() => setPaletteOpen(true)}>
            ⌘K
          </button>
        </nav>
        {attention.data && (
          <div className="overflow-x-auto">
            <GroupChips
              compact
              groups={groups}
              active={parseGroupFilter(search)}
              hotkeys={groupHotkeys(groups)}
            />
          </div>
        )}
      </div>
      <aside className="hidden border-r md:block">
        {attention.isPending && <p className="p-3 text-sm">loading…</p>}
        {attention.isError && (
          <p className="p-3 text-sm text-red-700">error: {attention.error.message}</p>
        )}
        {config.isError && (
          <p className="p-3 text-sm text-red-700">config: {config.error.message}</p>
        )}
        {attention.data && <GroupSidebar groups={groups} layout={layout} asleep={asleep} />}
        {layout.variant !== 'rail' && (
          <p className="p-3 text-xs text-gray-400">
            <kbd>?</kbd> shortcuts · <kbd>⌘K</kbd> palette
          </p>
        )}
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
