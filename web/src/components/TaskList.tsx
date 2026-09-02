import { Link } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'

import type { TaskSummary } from '../api/types'
import type { StatusGroup } from '../lib/groupByStatus'

// Presentation only: already-grouped, already-sorted tasks, one section per
// status; each row links to the task. The keyboard selection is a prop -
// which row is selected is the route's state, this only draws it.

interface Props {
  groups: StatusGroup[]
  selectedId?: string
}

export function TaskList({ groups, selectedId }: Props) {
  return (
    <div className="space-y-6">
      {groups.map((g) => (
        <section key={g.status} aria-label={g.status}>
          <h2 className="mb-2 border-b font-semibold">
            {g.status} <span className="font-normal text-gray-500">({g.tasks.length})</span>
          </h2>
          {g.tasks.length === 0 ? (
            <p className="text-sm text-gray-400">no tasks</p>
          ) : (
            <ul className="space-y-1">
              {g.tasks.map((t) => (
                <TaskRow key={t.id} task={t} selected={t.id === selectedId} />
              ))}
            </ul>
          )}
        </section>
      ))}
    </div>
  )
}

function TaskRow({ task, selected }: { task: TaskSummary; selected: boolean }) {
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView?.({ block: 'nearest' })
  }, [selected])
  return (
    <li
      ref={ref}
      aria-current={selected ? 'true' : undefined}
      className={`rounded px-1 ${selected ? 'bg-yellow-100' : ''}`}
    >
      <Link
        to="/p/$slug/t/$id"
        params={{ slug: task.project, id: task.id }}
        className="flex flex-wrap items-baseline gap-2 hover:underline"
      >
        <code className="text-sm text-gray-500">{task.id}</code>
        <span>{task.title}</span>
        {task.tags?.map((tag) => (
          <span key={tag} className="rounded bg-gray-100 px-1 text-xs text-gray-600">
            {tag}
          </span>
        ))}
      </Link>
    </li>
  )
}
