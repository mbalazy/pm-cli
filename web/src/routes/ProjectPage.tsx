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
    if (projects.isPending) return <p className="text-ink-3">loading…</p>
    if (projects.isError) return <p className="text-crit">error: {projects.error.message}</p>
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
    <div className="md:grid md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] md:gap-8">
      <div className="hidden md:block md:border-r md:border-rule md:pr-6">
        <h1 className="display mb-4 text-xl">{project?.name ?? slug}</h1>
        <BoardPane slug={slug} />
      </div>
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  )
}
