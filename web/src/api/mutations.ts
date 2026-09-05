import { useMutation, useQueryClient } from '@tanstack/react-query'

import { apiPost } from './client'
import { keys } from './queries'
import type {
  ConfigResult,
  ProjectResult,
  Report,
  ReportState,
  RunActionResult,
  RunFlags,
  SettingsPatch,
  TaskDetail,
  ToggleFocusResult,
  UpdateProjectBody,
  UpdateTaskBody,
} from './types'

// The mutation batch of pm-cli-118-16. Each request is one POST into the
// service function the MCP tool runs; after it lands the queries that read
// the task are invalidated here, explicitly - the SSE `tasks` event will
// come too, but a mutation must not depend on it.

/** What a confirmed action sends - decided by lib/confirmText, executed here. */
export type MutationRequest =
  | { kind: 'task'; project: string; taskId: string; body: UpdateTaskBody }
  | { kind: 'focus'; project: string; taskId: string }
  | { kind: 'seen'; ts?: string }
  | { kind: 'project'; project: string; body: UpdateProjectBody }
  | { kind: 'settings'; body: SettingsPatch }
  | { kind: 'run'; project: string; taskId: string; action: string; flags: RunFlags }
  | { kind: 'report_write' }
  | { kind: 'report_dismiss'; id: string }

export type MutationResult =
  | TaskDetail
  | ToggleFocusResult
  | ProjectResult
  | ConfigResult
  | RunActionResult
  | ReportState
  | Report
  | { seen: string }

export function runMutation(req: MutationRequest): Promise<MutationResult> {
  switch (req.kind) {
    case 'task':
      return apiPost<TaskDetail>(
        `/api/tasks/${encodeURIComponent(req.project)}/${encodeURIComponent(req.taskId)}`,
        req.body,
      )
    case 'focus':
      return apiPost<ToggleFocusResult>('/api/focus/toggle', { task_id: req.taskId })
    case 'seen':
      return apiPost<{ seen: string }>('/api/changes/seen', req.ts ? { ts: req.ts } : undefined)
    case 'project':
      return apiPost<ProjectResult>(`/api/projects/${encodeURIComponent(req.project)}`, req.body)
    case 'settings':
      return apiPost<ConfigResult>('/api/settings', req.body)
    case 'report_write':
      return apiPost<ReportState>('/api/report')
    case 'report_dismiss':
      return apiPost<Report>('/api/report/dismiss', { id: req.id })
    case 'run':
      return apiPost<RunActionResult>(
        `/api/runs/${encodeURIComponent(req.project)}/${encodeURIComponent(req.taskId)}/${req.action}`,
        req.flags,
      )
  }
}

/** One mutation for every confirmed action; invalidates what the request touched. */
export function useRowMutation() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: runMutation,
    onSuccess: (_res, req) => {
      const inv = (key: readonly unknown[]) => void client.invalidateQueries({ queryKey: key })
      inv(['attention'])
      if (req.kind === 'task' || req.kind === 'focus') {
        inv(keys.tasks(req.project))
        inv(['task', req.project])
        inv(keys.context(req.project))
        inv(keys.focus())
      }
      if (req.kind === 'seen') inv(keys.changes())
      if (req.kind === 'project') {
        inv(keys.projects())
        inv(keys.groups())
        inv(keys.context(req.project))
      }
      if (req.kind === 'report_write' || req.kind === 'report_dismiss') inv(keys.report())
      if (req.kind === 'run') {
        // A spawn seeds a run-state, a kill stamps one, a claim writes a
        // claim file: the runs table and the queue read all three.
        inv(keys.runs())
        inv(keys.tasks(req.project))
        inv(keys.context(req.project))
      }
      if (req.kind === 'settings') {
        // The sidebar, the sections and the thresholds all come off the
        // config; the queue and the feed are computed from it.
        inv(keys.config())
        inv(keys.projects())
        inv(keys.groups())
        inv(keys.changes())
      }
    },
  })
}
