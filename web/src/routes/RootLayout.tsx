import { Outlet, useParams } from '@tanstack/react-router'

import { useProjects } from '../api/queries'
import { ProjectSidebar } from '../components/ProjectSidebar'
import { openTaskCount } from '../lib/groupByStatus'

// Routes compose: they call api/ hooks and hand the data to components/.
// No domain logic lives here either - that is lib/.

export function RootLayout() {
  const { slug } = useParams({ strict: false })
  const projects = useProjects()

  return (
    <div className="grid min-h-screen grid-cols-[14rem_1fr]">
      <aside className="border-r">
        {projects.isPending && <p className="p-3 text-sm">loading…</p>}
        {projects.isError && (
          <p className="p-3 text-sm text-red-700">error: {projects.error.message}</p>
        )}
        {projects.data && (
          <ProjectSidebar
            activeSlug={slug}
            items={projects.data.projects
              .filter((p) => !p.archived)
              .map((p) => ({
                slug: p.slug,
                name: p.name,
                openTasks: openTaskCount(p.task_counts),
              }))}
          />
        )}
      </aside>
      <main className="p-4">
        <Outlet />
      </main>
    </div>
  )
}
