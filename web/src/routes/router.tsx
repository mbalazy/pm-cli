import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'

import { ProjectPage } from './ProjectPage'
import { RootLayout } from './RootLayout'
import { RunsPage } from './RunsPage'
import { TaskPage } from './TaskPage'

// Code-based routing (decision: a handful of routes do not justify the
// file-based plugin and its generated route tree). Only route definitions
// live here. `/p/$slug` is a layout route: the task detail renders in its
// Outlet, next to the list on a wide screen and instead of it on a phone.

const rootRoute = createRootRoute({ component: RootLayout })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <p className="text-gray-500">Select a project, or press ⌘K.</p>,
})

export const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/p/$slug',
  component: () => <ProjectPage slug={projectRoute.useParams().slug} />,
})

export const taskRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/t/$id',
  component: () => {
    const { slug, id } = taskRoute.useParams()
    return <TaskPage slug={slug} id={id} />
  },
})

const runsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/runs',
  component: RunsPage,
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  projectRoute.addChildren([taskRoute]),
  runsRoute,
])

/** Tests hand in a memory history; the browser uses the default. */
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
