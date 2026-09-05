import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'

import type { GroupSummary } from '../api/types'
import { severityGlyph } from '../lib/glyphs'
import { groupSearch } from '../lib/groupFilter'
import { groupTarget } from '../lib/sidebarView'
import { toneClass } from './tone'

// The home filter as pills: "all" plus one per group, each a link to `/?g=`.
// The active one is the URL's; the hotkey letters come from lib/groupFilter.
// The compact strip (the phone bar) also carries, per group, the one link the
// phone otherwise loses with the sidebar: the group page itself (ux-audit
// F-07 - "where we left off" had no tap path from Today).

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
  return (
    <nav
      aria-label="Group filter"
      className={compact ? 'flex items-center gap-1' : 'flex flex-wrap items-center gap-1'}
    >
      {!compact && <span className="kicker mr-1">show only</span>}
      <Link
        to={to}
        search={groupSearch('')}
        className="pill"
        data-on={active === ''}
        activeOptions={{ exact: true, includeSearch: true }}
      >
        all
      </Link>
      {groups.map((g) => (
        <Link
          key={g.slug}
          to={to}
          search={groupSearch(g.slug)}
          className="pill"
          data-on={active === g.slug}
          activeOptions={{ exact: true, includeSearch: true }}
          title={letterOf.has(g.slug) ? `g ${letterOf.get(g.slug)}` : undefined}
        >
          <span className={active === g.slug ? '' : toneClass(g.worst)} aria-hidden="true">
            {severityGlyph(g.worst)}
          </span>{' '}
          {g.name}
        </Link>
      ))}
      {compact &&
        groups.map((g) => (
          <Link
            key={`open:${g.slug}`}
            to={groupTarget(g).to}
            params={groupTarget(g).params}
            search={{ tab: 'overview' }}
            className="pill"
            aria-label={`open ${g.name}`}
            title={`open ${g.name}`}
          >
            <ArrowUpRight aria-hidden="true" className="size-3" strokeWidth={1.75} />
          </Link>
        ))}
      {!compact && to === '/' && (
        <span className="ml-1 text-xs text-ink-3 italic">one click, home stays</span>
      )}
    </nav>
  )
}
