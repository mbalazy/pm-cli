import { Navigate, Outlet, useParams } from '@tanstack/react-router'

import { useProjects } from '../api/queries'
import { groupOf } from '../lib/groupView'
import { BoardPane } from './BoardPane'

// `/p/$slug` is an ALIAS now (pm-cli-118-17): with no task open it redirects
// to the repo's group page, Board tab, so old links and the palette keep
// working; with a task open it is the detail's layout - the board beside the
// detail on a wide screen, the detail alone on a phone.

export function ProjectPage({ slug }: { slug: string }) {
  const projects = useProjects()
  const { id: openId } = useParams({ strict: false })

  if (!openId) {
    if (projects.isPending) return <p>loading…</p>
    if (projects.isError) return <p className="text-red-700">error: {projects.error.message}</p>
    return (
      <Navigate
        to="/g/$group"
        params={{ group: groupOf(projects.data.projects, slug) }}
        search={{ tab: 'board', repo: slug }}
        replace
      />
    )
  }

  const project = projects.data?.projects.find((p) => p.slug === slug)
  return (
    <div className="md:grid md:grid-cols-2 md:gap-6">
      <div className="hidden md:block">
        <h1 className="mb-4 text-xl font-bold">{project?.name ?? slug}</h1>
        <BoardPane slug={slug} />
      </div>
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  )
}
