import { Link } from '@tanstack/react-router'

import type { GroupSummary } from '../api/types'
import { severityGlyph } from '../lib/glyphs'
import { groupSearch } from '../lib/groupFilter'

// The home filter as chips: "all" plus one per group, each a link to `/?g=`.
// The active one is the URL's; the hotkey letters come from lib/groupFilter.

interface Props {
  groups: Pick<GroupSummary, 'slug' | 'name' | 'worst'>[]
  active: string
  hotkeys: Map<string, string>
  /** Smaller chips without the hint text (the phone bar). */
  compact?: boolean
  /** The screen the chips filter: home by default, or the Changes screen. */
  to?: '/' | '/changes'
}

export function GroupChips({ groups, active, hotkeys, compact, to = '/' }: Props) {
  const letterOf = new Map<string, string>()
  for (const [letter, slug] of hotkeys) letterOf.set(slug, letter)
  const chip = 'rounded border px-2 py-0.5 text-xs whitespace-nowrap hover:underline'
  return (
    <nav aria-label="Group filter" className="flex flex-wrap items-center gap-1">
      {!compact && <span className="text-xs text-gray-500">show only:</span>}
      <Link
        to={to}
        search={groupSearch('')}
        className={`${chip} ${active === '' ? 'font-bold' : ''}`}
        activeOptions={{ exact: true, includeSearch: true }}
      >
        all
      </Link>
      {groups.map((g) => (
        <Link
          key={g.slug}
          to={to}
          search={groupSearch(g.slug)}
          className={`${chip} ${active === g.slug ? 'font-bold' : ''}`}
          activeOptions={{ exact: true, includeSearch: true }}
          title={letterOf.has(g.slug) ? `g ${letterOf.get(g.slug)}` : undefined}
        >
          {severityGlyph(g.worst)} {g.name}
        </Link>
      ))}
      {!compact && to === '/' && (
        <span className="text-xs text-gray-400">one click, home stays</span>
      )}
    </nav>
  )
}
