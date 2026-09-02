import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'

import { ProjectPage } from './ProjectPage'
import { RootLayout } from './RootLayout'

// Code-based routing (decision: three routes do not justify the file-based
// plugin and its generated route tree). Only route definitions live here.

const rootRoute = createRootRoute({ component: RootLayout })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <p className="text-gray-500">Select a project.</p>,
})

const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/p/$slug',
  component: () => <ProjectPage slug={projectRoute.useParams().slug} />,
})

const routeTree = rootRoute.addChildren([indexRoute, projectRoute])

/** Tests hand in a memory history; the browser uses the default. */
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
