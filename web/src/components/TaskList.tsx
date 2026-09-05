import { Link } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'

import type { TaskSummary } from '../api/types'
import type { StatusGroup } from '../lib/groupByStatus'
import { SectionHead } from './SectionHead'

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
          <SectionHead title={g.status} count={`(${g.tasks.length})`} />
          {g.tasks.length === 0 ? (
            <p className="px-2 py-1 text-sm text-ink-3">no tasks</p>
          ) : (
            <ul>
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
    <li ref={ref} aria-current={selected ? 'true' : undefined} className="ledger-row px-2 py-1">
      <Link
        to="/p/$slug/t/$id"
        params={{ slug: task.project, id: task.id }}
        className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 hover:underline"
      >
        <span className="id">{task.id}</span>
        <span>{task.title}</span>
        {task.tags?.map((tag) => (
          <span key={tag} className="chip">
            {tag}
          </span>
        ))}
      </Link>
    </li>
  )
}
