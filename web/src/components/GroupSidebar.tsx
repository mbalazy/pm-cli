import { Link } from '@tanstack/react-router'

import type { GroupSummary } from '../api/types'
import { COUNTER_COLUMNS, severityGlyph } from '../lib/glyphs'
import { SCREENS } from '../lib/screens'
import type { SidebarLayout } from '../lib/sidebarView'
import { groupTarget } from '../lib/sidebarView'

// Presentation only: the screen navigation and the group list with the four
// counter columns. Which groups, in what order, with what counts, and which
// variant to draw are all decided upstream (the API and lib/sidebarView).

interface Props {
  groups: GroupSummary[]
  layout: SidebarLayout
  /** Asleep (archived) project slugs - one summary line, never rows. */
  asleep: string[]
  /** Called after any link is followed (a phone drawer closes itself). */
  onNavigate?: () => void
}

export function GroupSidebar({ groups, layout, asleep, onNavigate }: Props) {
  const rail = layout.variant === 'rail'
  return (
    <div className="p-2 text-sm">
      <nav aria-label="Screens" className="mb-3">
        <ul className={rail ? 'space-y-1 text-center' : 'space-y-0.5'}>
          {SCREENS.map((s) => (
            <li key={s.to}>
              <Link
                to={s.to}
                onClick={onNavigate}
                title={rail ? `${s.label} (${s.key})` : undefined}
                className="block rounded px-2 py-0.5 hover:underline"
                activeProps={{ className: 'font-bold' }}
                activeOptions={{ exact: s.to === '/' }}
              >
                {rail ? s.key : s.label}{' '}
                {!rail && <kbd className="text-xs text-gray-400">{s.key}</kbd>}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <nav aria-label="Projects">
        {!rail && (
          <h2 className="mb-1 px-2 text-xs uppercase text-gray-500">Projects · worst first</h2>
        )}
        <table aria-label="Groups" className="w-full">
          {layout.showCounts && (
            <thead>
              <tr className="text-xs text-gray-400">
                <th className="w-5" />
                <th />
                {COUNTER_COLUMNS.map((c) => (
                  <th key={c.key} className="w-6 text-right font-normal" title={c.title}>
                    {c.glyph}
                  </th>
                ))}
              </tr>
            </thead>
          )}
          <tbody>
            {groups.map((g) => (
              <GroupRows key={g.slug} group={g} layout={layout} onNavigate={onNavigate} />
            ))}
          </tbody>
        </table>
        {groups.length === 0 && <p className="px-2 text-xs text-gray-400">no active projects</p>}
        {asleep.length > 0 && (
          <p className="mt-2 px-2 text-xs text-gray-400" title={asleep.join(', ')}>
            {rail ? `zz ${asleep.length}` : `asleep (${asleep.length}) hidden`}
          </p>
        )}
      </nav>
    </div>
  )
}

function GroupRows({
  group,
  layout,
  onNavigate,
}: {
  group: GroupSummary
  layout: SidebarLayout
  onNavigate?: () => void
}) {
  const target = groupTarget(group)
  const counts = [group.failed, group.visual, group.waiting, group.quiet]
  return (
    <>
      <tr data-severity={group.worst}>
        <td className="w-5 text-center" aria-label={`worst: ${group.worst}`}>
          {severityGlyph(group.worst)}
        </td>
        <td className={layout.showNames ? '' : 'hidden'}>
          <Link
            to={target.to}
            params={target.params}
            onClick={onNavigate}
            className="block truncate px-1 hover:underline"
          >
            {group.name}
          </Link>
        </td>
        {!layout.showNames && (
          <td className="sr-only">
            <Link to={target.to} params={target.params} onClick={onNavigate} title={group.name}>
              {group.name}
            </Link>
          </td>
        )}
        {layout.showCounts &&
          counts.map((n, i) => (
            <td
              key={COUNTER_COLUMNS[i].key}
              className={`w-6 text-right tabular-nums ${n === 0 ? 'text-gray-300' : ''}`}
              title={COUNTER_COLUMNS[i].title}
            >
              {n === 0 ? '·' : n}
            </td>
          ))}
      </tr>
      {layout.showRepos &&
        group.projects.length > 1 &&
        group.projects.map((slug) => (
          <tr key={slug} className="text-xs text-gray-500">
            <td />
            <td colSpan={layout.showCounts ? 5 : 1}>
              <Link
                to="/p/$slug"
                params={{ slug }}
                onClick={onNavigate}
                className="block truncate pl-3 hover:underline"
              >
                · {slug}
              </Link>
            </td>
          </tr>
        ))}
    </>
  )
}
