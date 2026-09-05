import { Link } from '@tanstack/react-router'
import {
  Activity,
  CircleDashed,
  CircleX,
  Eye,
  Hourglass,
  Newspaper,
  Play,
  Settings2,
  type LucideProps,
} from 'lucide-react'

import { cn } from '@/components/ui/cn'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

import type { GroupSummary } from '../api/types'
import { COUNTER_COLUMNS, severityGlyph } from '../lib/glyphs'
import { SCREENS } from '../lib/screens'
import type { SidebarLayout } from '../lib/sidebarView'
import { groupTarget, repoTarget } from '../lib/sidebarView'
import { Glyph } from './Glyph'

// Presentation only: the screen navigation and the group list with the four
// counter columns - the ledger's index column. Which groups, in what order,
// with what counts, and which variant to draw are all decided upstream (the
// API and lib/sidebarView).

interface Props {
  groups: GroupSummary[]
  layout: SidebarLayout
  /** Asleep (archived) project slugs - one summary line, never rows. */
  asleep: string[]
  /** Called after any link is followed (a phone drawer closes itself). */
  onNavigate?: () => void
}

/** The four counter columns as icons (the text glyphs of lib/glyphs stay the
 *  vocabulary everywhere else); each header cell and each count carries the
 *  column's description as an instant tooltip. */
const COUNTER_ICON: Record<string, React.ComponentType<LucideProps>> = {
  failed: CircleX,
  visual: Eye,
  waiting: Hourglass,
  quiet: CircleDashed,
}
const COUNTER_TONE: Record<string, string> = {
  failed: 'sev-crit',
  visual: 'sev-warn',
  waiting: 'sev-info',
  quiet: 'sev-none',
}

const SCREEN_ICON: Record<string, React.ComponentType<LucideProps>> = {
  '/': Newspaper,
  '/changes': Activity,
  '/runs': Play,
  '/settings': Settings2,
}

export function GroupSidebar({ groups, layout, asleep, onNavigate }: Props) {
  const rail = layout.variant === 'rail'
  return (
    <div className={cn('flex flex-col gap-5 py-3 text-sm', rail ? 'px-1' : 'px-2')}>
      <nav aria-label="Screens">
        <ul className="space-y-px">
          {SCREENS.map((s) => {
            const Icon = SCREEN_ICON[s.to]
            return (
              <li key={s.to}>
                <Link
                  to={s.to}
                  onClick={onNavigate}
                  title={rail ? `${s.label} (${s.key})` : undefined}
                  className={cn(
                    'flex items-center gap-2 rounded-sm py-1 text-ink-2 hover:text-ink',
                    rail ? 'justify-center px-0' : 'px-2',
                    'border-l-2 border-transparent',
                  )}
                  activeProps={{ className: 'text-ink border-ink! font-medium bg-paper-2' }}
                  activeOptions={{ exact: s.to === '/' }}
                >
                  {Icon && (
                    <Icon aria-hidden="true" className="size-4 shrink-0" strokeWidth={1.75} />
                  )}
                  {rail ? (
                    <span className="sr-only">{s.label}</span>
                  ) : (
                    <>
                      <span>{s.label}</span>
                      <kbd className="ml-auto text-[0.6875rem] text-ink-3">{s.key}</kbd>
                    </>
                  )}
                </Link>
              </li>
            )
          })}
        </ul>
      </nav>
      <nav aria-label="Projects">
        {!rail && <h2 className="kicker mb-1.5 px-2">Projects · worst first</h2>}
        <table aria-label="Groups" className="w-full border-collapse">
          {layout.showCounts && (
            <thead>
              <tr>
                <th className="w-5" />
                <th />
                {COUNTER_COLUMNS.map((c) => {
                  const Icon = COUNTER_ICON[c.key]
                  return (
                    <th key={c.key} className="w-6 pb-1 text-right font-normal">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span
                            className={cn('inline-flex cursor-help', COUNTER_TONE[c.key])}
                            aria-label={c.title}
                            role="img"
                          >
                            <Icon aria-hidden="true" className="size-3.5" strokeWidth={1.75} />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent side="bottom">
                          {c.glyph} {c.title}
                        </TooltipContent>
                      </Tooltip>
                    </th>
                  )
                })}
              </tr>
            </thead>
          )}
          <tbody>
            {groups.map((g) => (
              <GroupRows key={g.slug} group={g} layout={layout} onNavigate={onNavigate} />
            ))}
          </tbody>
        </table>
        {layout.showCounts && groups.length > 0 && (
          <p className="mt-2 flex flex-wrap gap-x-2 gap-y-0.5 px-2 text-[0.6875rem] text-ink-3">
            {COUNTER_COLUMNS.map((c) => {
              const Icon = COUNTER_ICON[c.key]
              return (
                <span key={c.key} className="inline-flex items-center gap-0.5" title={c.title}>
                  <Icon aria-hidden="true" className="size-3" strokeWidth={1.75} />
                  {c.key}
                </span>
              )
            })}
          </p>
        )}
        {groups.length === 0 && <p className="px-2 text-xs text-ink-3">no active projects</p>}
        {asleep.length > 0 && (
          <p className="mt-2 px-2 text-xs text-ink-3" title={asleep.join(', ')}>
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
  const tone = ['crit', 'warn', 'info', 'none']
  return (
    <>
      <tr data-severity={group.worst} className="group/row">
        <td className="w-5 py-0.5 text-center">
          <Glyph
            glyph={severityGlyph(group.worst)}
            severity={group.worst}
            label={`worst: ${group.worst}`}
          />
        </td>
        <td className={layout.showNames ? 'max-w-0 py-0.5' : 'hidden'}>
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
              className={cn(
                'num w-6 py-0.5 text-right',
                n === 0 ? 'text-ink-3/60' : `sev-${tone[i]} font-medium`,
              )}
            >
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="cursor-help">{n === 0 ? '·' : n}</span>
                </TooltipTrigger>
                <TooltipContent side="right">
                  {group.name}: {n} {COUNTER_COLUMNS[i].title}
                </TooltipContent>
              </Tooltip>
            </td>
          ))}
      </tr>
      {layout.showRepos &&
        group.projects.length > 1 &&
        group.projects.map((slug) => (
          <tr key={slug} className="text-xs text-ink-3">
            <td />
            <td colSpan={layout.showCounts ? 5 : 1} className="max-w-0">
              <Link
                {...repoTarget(group.slug, slug)}
                onClick={onNavigate}
                className="block truncate pl-3 hover:text-ink hover:underline"
              >
                · {slug}
              </Link>
            </td>
          </tr>
        ))}
    </>
  )
}
