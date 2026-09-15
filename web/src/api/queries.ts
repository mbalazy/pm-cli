// react-query hooks, one per endpoint. The KEYS are a contract shared with the
// live feed (118-9 invalidates by them) - keep them exactly as listed:
//   ['projects'] ['groups'] ['tasks', slug] ['task', slug, id] ['context', slug] ['runs'] ['focus']
//   ['attention', project, group] ['changes'] ['config'] ['timeline', slug]
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiGet, apiPost, query } from './client'
import type {
  Attention,
  Changes,
  ConfigResult,
  CrossProjectContextResult,
  FocusResult,
  GroupsResult,
  ListTasksResult,
  ProjectContextResult,
  ProjectsResult,
  RefreshResult,
  ReportState,
  Review,
  ReviewsResult,
  RunFlags,
  RunPlan,
  RunsResult,
  SeenResult,
  SoloReportResult,
  SoloResult,
  TaskDetail,
  TimelineRead,
} from './types'

/** The API's hard cap on `limit` (service.MaxListLimit). */
export const MAX_LIST_LIMIT = 200

export const keys = {
  projects: () => ['projects'] as const,
  groups: () => ['groups'] as const,
  tasks: (slug: string) => ['tasks', slug] as const,
  task: (slug: string, id: string) => ['task', slug, id] as const,
  context: (slug: string) => ['context', slug] as const,
  timeline: (slug: string) => ['timeline', slug] as const,
  runs: () => ['runs'] as const,
  /** Deliberately NOT under ['runs']: a `runs` event must never trigger an ssh round-trip. */
  runsRemote: () => ['runs-remote'] as const,
  focus: () => ['focus'] as const,
  attention: (project = '', group = '') => ['attention', project, group] as const,
  changes: () => ['changes'] as const,
  config: () => ['config'] as const,
  report: () => ['report'] as const,
  solo: () => ['solo'] as const,
  reviews: () => ['reviews'] as const,
  review: (id: string) => ['review', id] as const,
  soloReport: (project: string, shift: string) => ['solo-report', project, shift] as const,
  runPlan: (project: string, id: string, action: string, flags: RunFlags) =>
    ['run-plan', project, id, action, flags.yolo === true, flags.additional === true] as const,
}

export function useProjects() {
  return useQuery({
    queryKey: keys.projects(),
    queryFn: () => apiGet<ProjectsResult>('/api/projects'),
  })
}

/** The cockpit's project groups (sidebar input); archived projects are in none. */
export function useGroups() {
  return useQuery({
    queryKey: keys.groups(),
    queryFn: () => apiGet<GroupsResult>('/api/groups'),
  })
}

/** The query options of one project's task list - shared by useTasks and the group page's useQueries. */
export function tasksQuery(slug: string) {
  return {
    queryKey: keys.tasks(slug),
    queryFn: () =>
      apiGet<ListTasksResult>(`/api/tasks${query({ project: slug, limit: MAX_LIST_LIMIT })}`),
    enabled: slug !== '',
  }
}

/** All non-archived tasks of one project (up to the API's cap), newest first. */
export function useTasks(slug: string) {
  return useQuery(tasksQuery(slug))
}

/** The query options of one project's context (trackers, doing tasks, counts). */
export function contextQuery(slug: string) {
  return {
    queryKey: keys.context(slug),
    queryFn: () => apiGet<ProjectContextResult>(`/api/context${query({ project: slug })}`),
    enabled: slug !== '',
  }
}

/** The query options of one project's timeline: the latest state and the entries after it. */
export function timelineQuery(slug: string) {
  return {
    queryKey: keys.timeline(slug),
    queryFn: () => apiGet<TimelineRead>(`/api/timeline/${encodeURIComponent(slug)}`),
    enabled: slug !== '',
  }
}

export function useTimeline(slug: string) {
  return useQuery(timelineQuery(slug))
}

export function useTask(slug: string, id: string) {
  return useQuery({
    queryKey: keys.task(slug, id),
    queryFn: () =>
      apiGet<TaskDetail>(`/api/tasks/${encodeURIComponent(slug)}/${encodeURIComponent(id)}`),
    enabled: slug !== '' && id !== '',
  })
}

export function useProjectContext(slug: string) {
  return useQuery(contextQuery(slug))
}

export function useCrossProjectContext() {
  return useQuery({
    queryKey: keys.context(''),
    queryFn: () => apiGet<CrossProjectContextResult>('/api/context'),
  })
}

/** Local run rows only; the remote (ssh) fetch is a separate, explicit call. */
export function useRuns() {
  return useQuery({
    queryKey: keys.runs(),
    queryFn: () => apiGet<RunsResult>('/api/runs'),
  })
}

/**
 * Local + remote rows (`?remote=1`, one ssh round-trip per runner). Disabled:
 * nothing fetches it until the caller's `refetch()` - a button, never a poll.
 */
export function useRemoteRuns() {
  return useQuery({
    queryKey: keys.runsRemote(),
    queryFn: () => apiGet<RunsResult>('/api/runs?remote=1'),
    enabled: false,
    staleTime: Infinity,
    retry: false,
  })
}

export function useFocus() {
  return useQuery({
    queryKey: keys.focus(),
    queryFn: () => apiGet<FocusResult>('/api/focus'),
  })
}

/** The resolved cockpit config (sidebar variant, section toggles, refresh schedule). */
export function useConfig() {
  return useQuery({
    queryKey: keys.config(),
    queryFn: () => apiGet<ConfigResult>('/api/config'),
  })
}

/** The attention queue (home screen), optionally scoped to one project or one group. */
export function useAttention(project = '', group = '') {
  return useQuery({
    queryKey: keys.attention(project, group),
    queryFn: () => apiGet<Attention>(`/api/attention${query({ project, group })}`),
  })
}

/** The change feed since the cutoff, off the server's cache - never a fetch of the sources. */
export function useChanges() {
  return useQuery({
    queryKey: keys.changes(),
    queryFn: () => apiGet<Changes>('/api/changes'),
  })
}

/** "Fetch now": runs the sources whatever the refresh window says. The SSE `changes` event refetches. */
export function useRefreshChanges() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => apiPost<RefreshResult>('/api/changes/refresh'),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.changes() })
      void client.invalidateQueries({ queryKey: ['attention'] })
    },
  })
}

/** Marks everything up to `ts` (default: now) as read. */
export function useMarkChangesSeen() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (ts?: string) => apiPost<SeenResult>('/api/changes/seen', ts ? { ts } : undefined),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.changes() })
      void client.invalidateQueries({ queryKey: ['attention'] })
    },
  })
}

/**
 * What a run action would do (argv, warnings, the kill target) - the
 * dialog's preview. Disabled until an action is pending; never cached long,
 * since the run-states it reads move.
 */
export function useRunPlan(project: string, id: string, action: string, flags: RunFlags) {
  return useQuery({
    queryKey: keys.runPlan(project, id, action, flags),
    queryFn: () =>
      apiGet<RunPlan>(
        `/api/runs/${encodeURIComponent(project)}/${encodeURIComponent(id)}/plan${query({
          action,
          yolo: flags.yolo ? 1 : undefined,
          additional: flags.additional ? 1 : undefined,
        })}`,
      ),
    enabled: project !== '' && id !== '' && action !== '',
    staleTime: 0,
    retry: false,
  })
}

/** The PR code reviews, newest first; polled every 5 s while one runs. */
export function useReviews() {
  return useQuery({
    queryKey: keys.reviews(),
    queryFn: () => apiGet<ReviewsResult>('/api/reviews'),
    refetchInterval: (q) =>
      q.state.data?.reviews.some((r) => r.state === 'running') ? 5000 : false,
  })
}

/** One review with its report; polled while it runs. */
export function useReview(id: string) {
  return useQuery({
    queryKey: keys.review(id),
    queryFn: () => apiGet<Review>(`/api/reviews/${encodeURIComponent(id)}`),
    enabled: id !== '',
    refetchInterval: (q) => (q.state.data?.state === 'running' ? 5000 : false),
  })
}

/** Every /solo shift across the active projects, open ones first, then newest. */
export function useSolo() {
  return useQuery({
    queryKey: keys.solo(),
    queryFn: () => apiGet<SoloResult>('/api/solo'),
  })
}

/** One shift's report (or its state file while there is none). */
export function useSoloReport(project: string, shift: string) {
  return useQuery({
    queryKey: keys.soloReport(project, shift),
    queryFn: () =>
      apiGet<SoloReportResult>(
        `/api/solo/${encodeURIComponent(project)}/${encodeURIComponent(shift)}/report`,
      ),
    enabled: project !== '' && shift !== '',
  })
}

/** The period's LLM report (off / none / writing / done / error) off the server's disk. */
export function useReport() {
  return useQuery({
    queryKey: keys.report(),
    queryFn: () => apiGet<ReportState>('/api/report'),
  })
}
