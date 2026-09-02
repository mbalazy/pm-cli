import { useProjects, useTasks } from '../api/queries'
import { TaskList } from '../components/TaskList'
import { groupByStatus } from '../lib/groupByStatus'

export function ProjectPage({ slug }: { slug: string }) {
  const projects = useProjects()
  const tasks = useTasks(slug)
  const project = projects.data?.projects.find((p) => p.slug === slug)

  if (tasks.isPending || projects.isPending) return <p>loading…</p>
  if (tasks.isError) return <p className="text-red-700">error: {tasks.error.message}</p>
  if (projects.isError) return <p className="text-red-700">error: {projects.error.message}</p>
  if (!project) return <p className="text-red-700">error: no such project: {slug}</p>

  return (
    <>
      <h1 className="mb-4 text-xl font-bold">{project.name}</h1>
      {tasks.data.note && <p className="mb-2 text-sm text-gray-500">{tasks.data.note}</p>}
      <TaskList groups={groupByStatus(tasks.data.tasks, project.statuses)} />
    </>
  )
}
