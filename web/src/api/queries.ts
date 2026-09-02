// react-query hooks, one per endpoint. The KEYS are a contract shared with the
// live feed (118-9 invalidates by them) - keep them exactly as listed:
//   ['projects'] ['tasks', slug] ['task', slug, id] ['context', slug] ['runs'] ['focus']
import { useQuery } from '@tanstack/react-query'

import { apiGet, query } from './client'
import type {
  CrossProjectContextResult,
  FocusResult,
  ListTasksResult,
  ProjectContextResult,
  ProjectsResult,
  RunsResult,
  TaskDetail,
} from './types'

/** The API's hard cap on `limit` (service.MaxListLimit). */
export const MAX_LIST_LIMIT = 200

export const keys = {
  projects: () => ['projects'] as const,
  tasks: (slug: string) => ['tasks', slug] as const,
  task: (slug: string, id: string) => ['task', slug, id] as const,
  context: (slug: string) => ['context', slug] as const,
  runs: () => ['runs'] as const,
  focus: () => ['focus'] as const,
}

export function useProjects() {
  return useQuery({
    queryKey: keys.projects(),
    queryFn: () => apiGet<ProjectsResult>('/api/projects'),
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

export function useFocus() {
  return useQuery({
    queryKey: keys.focus(),
    queryFn: () => apiGet<FocusResult>('/api/focus'),
  })
}
