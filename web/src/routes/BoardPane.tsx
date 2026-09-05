import { useNavigate, useParams } from '@tanstack/react-router'
import { useState } from 'react'

import { useProjects, useTasks } from '../api/queries'
import { TaskList } from '../components/TaskList'
import { useShortcuts } from '../hooks/useShortcuts'
import { groupByStatus } from '../lib/groupByStatus'

// One repo's board: the status columns of pm-cli-118-9 with the j/k/Enter
// selection. Used by the group page's Board tab and by the task detail's
// layout (the list beside the detail). Composition only.

export function BoardPane({ slug }: { slug: string }) {
  const projects = useProjects()
  const tasks = useTasks(slug)
  const navigate = useNavigate()
  const { id: openId } = useParams({ strict: false })
  const [selectedId, setSelectedId] = useState<string>()

  const project = projects.data?.projects.find((p) => p.slug === slug)
  const groups = project && tasks.data ? groupByStatus(tasks.data.tasks, project.statuses) : []
  const flat = groups.flatMap((g) => g.tasks)

  const step = (delta: number) => {
    if (flat.length === 0) return
    const i = flat.findIndex((t) => t.id === selectedId)
    const next =
      i === -1 ? (delta > 0 ? 0 : flat.length - 1) : (i + delta + flat.length) % flat.length
    setSelectedId(flat[next].id)
  }
  useShortcuts({
    down: () => step(1),
    up: () => step(-1),
    open: () => {
      if (selectedId) void navigate({ to: '/p/$slug/t/$id', params: { slug, id: selectedId } })
    },
  })

  if (tasks.isPending || projects.isPending) return <p className="text-ink-3">loading…</p>
  if (tasks.isError) return <p className="text-crit">error: {tasks.error.message}</p>
  if (projects.isError) return <p className="text-crit">error: {projects.error.message}</p>
  if (!project) return <p className="text-crit">error: no such project: {slug}</p>

  return (
    <div className={openId ? 'hidden md:block' : ''}>
      {tasks.data.note && <p className="mb-2 text-xs text-ink-3 italic">{tasks.data.note}</p>}
      <TaskList groups={groups} selectedId={selectedId} />
    </div>
  )
}
