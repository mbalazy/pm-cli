import { Link } from '@tanstack/react-router'

import { cn } from '@/components/ui/cn'

import type { TaskSummary, Tracker } from '../api/types'
import { progressText } from '../lib/doingView'
import { Glyph } from './Glyph'
import { SectionHead } from './SectionHead'

// The group's "in progress" list: already ordered upstream (lib/doingView),
// with the idle flag and the age decided there too. A tracker row shows its
// rollup progress. Presentation only.

export interface DoingRow {
  task: TaskSummary
  idle: boolean
  ageText: string
  tracker?: Tracker
}

interface Props {
  rows: DoingRow[]
  total: number
  showProject: boolean
  idleDays: number
}

export function DoingList({ rows, total, showProject, idleDays }: Props) {
  return (
    <section aria-label="In progress (doing)">
      <SectionHead
        title="In progress (doing)"
        count={total}
        why={`age = since the last change (edit or status) · no activity for ${idleDays}d = quiet`}
      />
      {rows.length === 0 ? (
        <p className="px-2 py-1 text-sm text-ink-3">Nothing on doing.</p>
      ) : (
        <ul>
          {rows.map(({ task, idle, ageText, tracker }) => (
            <li
              key={`${task.project}/${task.id}`}
              data-idle={idle || undefined}
              className={cn(
                'ledger-row grid grid-cols-[1.25rem_minmax(0,1fr)_4rem] items-baseline gap-x-2 px-2 py-1',
                idle && 'text-ink-2',
              )}
            >
              <Glyph
                glyph={idle ? '◔' : '●'}
                severity={idle ? undefined : 'info'}
                label={idle ? 'quiet' : 'active'}
              />
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                {showProject && <span className="chip">{task.project}</span>}
                <Link
                  to="/p/$slug/t/$id"
                  params={{ slug: task.project, id: task.id }}
                  className="hover:underline"
                >
                  <span className="id mr-1.5">{task.id}</span>
                  {task.title}
                </Link>
                {tracker && (
                  <span className="num text-ink-3">
                    tracker {progressText(tracker)} of {tracker.total}
                  </span>
                )}
                {task.waiting_for && (
                  <span className="text-xs text-ink-3">waiting for: {task.waiting_for}</span>
                )}
              </span>
              <span className="num text-right whitespace-nowrap text-ink-2">{ageText}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
