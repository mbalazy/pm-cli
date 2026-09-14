import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'

import { parseGroupFilter } from '../lib/groupFilter'
import { parseTab } from '../lib/groupView'
import { GroupPage } from './GroupPage'
import { ProjectPage } from './ProjectPage'
import { RootLayout } from './RootLayout'
import { RunsPage } from './RunsPage'
import { ChangesPage } from './ChangesPage'
import { ReviewDetailPage } from './ReviewDetailPage'
import { ReviewPage } from './ReviewPage'
import { SettingsPage } from './SettingsPage'
import { SoloReportPage } from './SoloReportPage'
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

// The group page (pm-cli-118-17): tab and board repo in the search so a tab
// is a URL. `/p/$slug` stays as the alias + the task detail's layout.
export const groupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/g/$group',
  validateSearch: (search: Record<string, unknown>): { tab?: string; repo?: string } => {
    const out: { tab?: string; repo?: string } = {}
    if (typeof search.tab === 'string' && search.tab !== 'overview') out.tab = parseTab(search.tab)
    if (typeof search.repo === 'string' && search.repo !== '') out.repo = search.repo
    return out
  },
  component: () => {
    const { group } = groupRoute.useParams()
    const { tab, repo } = groupRoute.useSearch()
    return <GroupPage group={group} tab={parseTab(tab)} repo={repo} />
  },
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

// The Changes screen (pm-cli-118-18): the group filter in the URL like home.
export const changesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/changes',
  validateSearch: (search: Record<string, unknown>): { g?: string } => {
    const g = parseGroupFilter(search)
    return g === '' ? {} : { g }
  },
  component: () => <ChangesPage group={changesRoute.useSearch().g ?? ''} />,
})

// PR code reviews: the list + start form, and one review's report.
const reviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/review',
  component: ReviewPage,
})

export const reviewDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/review/$id',
  component: () => <ReviewDetailPage id={reviewDetailRoute.useParams().id} />,
})

// One /solo shift's report (pm-cli-136).
export const soloRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/solo/$project/$shift',
  component: () => {
    const { project, shift } = soloRoute.useParams()
    return <SoloReportPage project={project} shift={shift} />
  },
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsPage,
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  groupRoute,
  projectRoute.addChildren([taskRoute]),
  runsRoute,
  changesRoute,
  reviewRoute,
  reviewDetailRoute,
  soloRoute,
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
