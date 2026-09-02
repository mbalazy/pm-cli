import type { TaskSummary } from '../api/types'
import type { StatusGroup } from '../lib/groupByStatus'

// Presentation only: already-grouped, already-sorted tasks, one section per
// status. No fetching, no grouping, no sorting here - that is lib/ and the
// route that composes it.

interface Props {
  groups: StatusGroup[]
}

export function TaskList({ groups }: Props) {
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
                <TaskRow key={t.id} task={t} />
              ))}
            </ul>
          )}
        </section>
      ))}
    </div>
  )
}

function TaskRow({ task }: { task: TaskSummary }) {
  return (
    <li className="flex flex-wrap items-baseline gap-2">
      <code className="text-sm text-gray-500">{task.id}</code>
      <span>{task.title}</span>
      {task.tags?.map((tag) => (
        <span key={tag} className="rounded bg-gray-100 px-1 text-xs text-gray-600">
          {tag}
        </span>
      ))}
    </li>
  )
}
