import { useMutation, useQueryClient } from '@tanstack/react-query'

import { apiPost } from './client'
import { keys } from './queries'
import type { ProjectResult, TaskDetail, ToggleFocusResult, UpdateTaskBody } from './types'

// The mutation batch of pm-cli-118-16. Each request is one POST into the
// service function the MCP tool runs; after it lands the queries that read
// the task are invalidated here, explicitly - the SSE `tasks` event will
// come too, but a mutation must not depend on it.

/** What a confirmed action sends - decided by lib/confirmText, executed here. */
export type MutationRequest =
  | { kind: 'task'; project: string; taskId: string; body: UpdateTaskBody }
  | { kind: 'focus'; project: string; taskId: string }
  | { kind: 'seen'; ts?: string }
  | { kind: 'project'; project: string; notes: string }

export type MutationResult = TaskDetail | ToggleFocusResult | ProjectResult | { seen: string }

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
      return apiPost<ProjectResult>(`/api/projects/${encodeURIComponent(req.project)}`, {
        notes: req.notes,
      })
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
        inv(keys.context(req.project))
      }
    },
  })
}
