import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'

import { parseGroupFilter } from '../lib/groupFilter'
import { ProjectPage } from './ProjectPage'
import { RootLayout } from './RootLayout'
import { RunsPage } from './RunsPage'
import { TaskPage } from './TaskPage'
import { TodayPage } from './TodayPage'

// Code-based routing (decision: a handful of routes do not justify the
// file-based plugin and its generated route tree). Only route definitions
// live here. `/` is Today (the attention queue, `?g=` narrows it to a group);
// `/p/$slug` is a layout route: the task detail renders in its Outlet, next
// to the list on a wide screen and instead of it on a phone.

const rootRoute = createRootRoute({ component: RootLayout })

export const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  validateSearch: (search: Record<string, unknown>): { g?: string } => {
    const g = parseGroupFilter(search)
    return g === '' ? {} : { g }
  },
  component: () => <TodayPage group={indexRoute.useSearch().g ?? ''} />,
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

// Screens 2 and 4 of the cockpit land in pm-cli-118-18; the routes exist so
// the sidebar and the 2 / , keys already lead somewhere honest.
const changesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/changes',
  component: () => <p className="text-gray-500">Changes - coming in pm-cli-118-18.</p>,
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: () => <p className="text-gray-500">Settings - coming in pm-cli-118-18.</p>,
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  projectRoute.addChildren([taskRoute]),
  runsRoute,
  changesRoute,
  settingsRoute,
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
