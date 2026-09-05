import { Link, Outlet, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { Monitor, Moon, Search, Sun } from 'lucide-react'
import { useState } from 'react'

import { useAttention, useConfig, useProjects, useTasks } from '../api/queries'
import { useLiveInvalidation } from '../api/useLiveInvalidation'
import { GroupChips } from '../components/GroupChips'
import { GroupSidebar } from '../components/GroupSidebar'
import { HelpDialog } from '../components/HelpDialog'
import { Palette } from '../components/Palette'
import { useRecentTasks } from '../hooks/useRecentTasks'
import { useShortcuts } from '../hooks/useShortcuts'
import { useTheme } from '../hooks/useTheme'
import { groupHotkeys, parseGroupFilter } from '../lib/groupFilter'
import { paletteItems, type PaletteItem } from '../lib/paletteItems'
import { SCREENS } from '../lib/screens'
import { groupMembers } from '../lib/groupView'
import { asleepProjects, sidebarLayout, sortGroups } from '../lib/sidebarView'

// The shell: the group sidebar (a top bar on a phone), the page, the
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
  const theme = useTheme()
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
  const columns = layout.width ?? (layout.variant === 'rail' ? '3rem' : '15rem')
  const ThemeIcon = theme.choice === 'dark' ? Moon : theme.choice === 'light' ? Sun : Monitor

  return (
    <div
      className="min-h-screen md:grid"
      style={{ gridTemplateColumns: `${columns} minmax(0, 1fr)` }}
    >
      {/* The phone bar: masthead, screens, the group strip. */}
      <div className="sticky top-0 z-20 border-b border-rule bg-paper/95 backdrop-blur-sm md:hidden">
        <div className="flex items-center gap-1 px-3 pt-2 pb-1">
          <span className="display mr-2 text-lg">pm</span>
          <nav aria-label="Screens" className="flex gap-0.5 text-sm">
            {SCREENS.map((s) => (
              <Link
                key={s.to}
                to={s.to}
                className="rounded-sm px-2 py-1 text-ink-2"
                activeProps={{ className: 'text-ink font-medium bg-paper-2' }}
                activeOptions={{ exact: s.to === '/' }}
              >
                {s.label}
              </Link>
            ))}
          </nav>
          <button
            type="button"
            className="ml-auto flex size-9 items-center justify-center rounded-sm text-ink-2"
            aria-label="Command palette"
            onClick={() => setPaletteOpen(true)}
          >
            <Search aria-hidden="true" className="size-4" strokeWidth={1.75} />
          </button>
        </div>
        {attention.data && (
          <div className="overflow-x-auto px-3 pb-2 [scrollbar-width:none]">
            <GroupChips
              compact
              groups={groups}
              active={parseGroupFilter(search)}
              hotkeys={groupHotkeys(groups)}
            />
          </div>
        )}
      </div>

      <aside className="hidden border-r border-rule bg-paper-2/40 md:flex md:flex-col md:sticky md:top-0 md:h-screen md:overflow-y-auto">
        <div className="display px-4 pt-4 text-xl">pm</div>
        {attention.isPending && <p className="p-3 text-sm text-ink-3">loading…</p>}
        {attention.isError && (
          <p className="p-3 text-sm text-crit">error: {attention.error.message}</p>
        )}
        {config.isError && <p className="p-3 text-sm text-crit">config: {config.error.message}</p>}
        {attention.data && <GroupSidebar groups={groups} layout={layout} asleep={asleep} />}
        <div className="mt-auto flex items-center gap-2 px-3 py-3 text-xs text-ink-3">
          {layout.variant !== 'rail' && (
            <span>
              <kbd>?</kbd> shortcuts · <kbd>⌘K</kbd> palette
            </span>
          )}
          <button
            type="button"
            className="ml-auto flex size-7 items-center justify-center rounded-sm text-ink-3 hover:bg-paper-3 hover:text-ink"
            title={`theme: ${theme.choice} (click to change)`}
            aria-label={`theme: ${theme.choice}`}
            onClick={theme.cycle}
          >
            <ThemeIcon aria-hidden="true" className="size-4" strokeWidth={1.75} />
          </button>
        </div>
      </aside>
      <main className="min-w-0 px-3 py-4 md:px-8 md:py-6">
        <div className="mx-auto max-w-6xl">
          <Outlet />
        </div>
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
