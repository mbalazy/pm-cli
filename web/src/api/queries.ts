// react-query hooks, one per endpoint. The KEYS are a contract shared with the
// live feed (118-9 invalidates by them) - keep them exactly as listed:
//   ['projects'] ['groups'] ['tasks', slug] ['task', slug, id] ['context', slug] ['runs'] ['focus']
//   ['attention', project, group] ['changes'] ['config']
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
  RunsResult,
  SeenResult,
  TaskDetail,
} from './types'

/** The API's hard cap on `limit` (service.MaxListLimit). */
export const MAX_LIST_LIMIT = 200

export const keys = {
  projects: () => ['projects'] as const,
  groups: () => ['groups'] as const,
  tasks: (slug: string) => ['tasks', slug] as const,
  task: (slug: string, id: string) => ['task', slug, id] as const,
  context: (slug: string) => ['context', slug] as const,
  runs: () => ['runs'] as const,
  /** Deliberately NOT under ['runs']: a `runs` event must never trigger an ssh round-trip. */
  runsRemote: () => ['runs-remote'] as const,
  focus: () => ['focus'] as const,
  attention: (project = '', group = '') => ['attention', project, group] as const,
  changes: () => ['changes'] as const,
  config: () => ['config'] as const,
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

/** All non-archived tasks of one project (up to the API's cap), newest first. */
export function useTasks(slug: string) {
  return useQuery({
    queryKey: keys.tasks(slug),
    queryFn: () =>
      apiGet<ListTasksResult>(`/api/tasks${query({ project: slug, limit: MAX_LIST_LIMIT })}`),
    enabled: slug !== '',
  })
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
  return useQuery({
    queryKey: keys.context(slug),
    queryFn: () => apiGet<ProjectContextResult>(`/api/context${query({ project: slug })}`),
    enabled: slug !== '',
  })
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
