import { Link } from '@tanstack/react-router'

import type { TaskSummary, Tracker } from '../api/types'
import { progressText } from '../lib/doingView'

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
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
        <span className="font-semibold">In progress (doing)</span>
        <span className="text-sm text-gray-500">{total}</span>
        <span className="text-xs text-gray-400">
          age = since the last change (edit or status) · no activity for {idleDays}d = quiet
        </span>
      </h2>
      {rows.length === 0 ? (
        <p className="text-sm text-gray-500">Nothing on doing.</p>
      ) : (
        <ul className="space-y-0.5">
          {rows.map(({ task, idle, ageText, tracker }) => (
            <li
              key={`${task.project}/${task.id}`}
              data-idle={idle || undefined}
              className={`flex flex-wrap items-baseline gap-x-2 rounded px-1 py-0.5 ${idle ? 'text-gray-500' : ''}`}
            >
              <span className="inline-block w-4 text-center" aria-label={idle ? 'quiet' : 'active'}>
                {idle ? '◔' : '●'}
              </span>
              {showProject && (
                <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">
                  {task.project}
                </span>
              )}
              <Link
                to="/p/$slug/t/$id"
                params={{ slug: task.project, id: task.id }}
                className="hover:underline"
              >
                <code className="mr-1 text-sm text-gray-500">{task.id}</code>
                {task.title}
              </Link>
              {tracker && (
                <span className="text-xs text-gray-500">
                  tracker {progressText(tracker)} of {tracker.total}
                </span>
              )}
              {task.waiting_for && (
                <span className="text-xs text-gray-500">waiting for: {task.waiting_for}</span>
              )}
              <span className="ml-auto text-sm tabular-nums text-gray-500">{ageText}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
