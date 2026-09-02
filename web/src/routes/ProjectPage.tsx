import { Outlet, useNavigate, useParams } from '@tanstack/react-router'
import { useState } from 'react'

import { useProjects, useTasks } from '../api/queries'
import { TaskList } from '../components/TaskList'
import { useShortcuts } from '../hooks/useShortcuts'
import { groupByStatus } from '../lib/groupByStatus'

export function ProjectPage({ slug }: { slug: string }) {
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

  if (tasks.isPending || projects.isPending) return <p>loading…</p>
  if (tasks.isError) return <p className="text-red-700">error: {tasks.error.message}</p>
  if (projects.isError) return <p className="text-red-700">error: {projects.error.message}</p>
  if (!project) return <p className="text-red-700">error: no such project: {slug}</p>

  return (
    <div className="md:grid md:grid-cols-2 md:gap-6">
      <div className={openId ? 'hidden md:block' : ''}>
        <h1 className="mb-4 text-xl font-bold">{project.name}</h1>
        {tasks.data.note && <p className="mb-2 text-sm text-gray-500">{tasks.data.note}</p>}
        <TaskList groups={groups} selectedId={selectedId} />
      </div>
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  )
}
