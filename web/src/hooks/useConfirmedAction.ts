import { useCallback, useEffect, useRef, useState } from 'react'

import { useRowMutation } from '../api/mutations'
import { useRunPlan } from '../api/queries'
import type { RunFlags, RunStarted } from '../api/types'
import {
  describeAction,
  initialValue,
  isRunKind,
  requestFor,
  type ConfirmText,
  type PendingAction,
} from '../lib/confirmText'

// React glue for "every action goes through a dialog": holds the pending
// action and the field value, runs the mutation on confirm, and keeps a
// short-lived toast. The wording and the request are lib/confirmText's.

export interface ConfirmedAction {
  pending: PendingAction | null
  text: ConfirmText | null
  value: string
  setValue: (v: string) => void
  /** Opens the dialog for an action; `focused` words the focus toggle. */
  ask: (p: PendingAction, focused?: boolean) => void
  confirm: () => void
  cancel: () => void
  busy: boolean
  error?: string
  toast: { message: string; error: boolean }
  /** A run action's launch flags (the dialog's checkboxes). */
  flags: RunFlags
  setFlags: (f: RunFlags) => void
}

const TOAST_MS = 3000

export function useConfirmedAction(): ConfirmedAction {
  const [pending, setPending] = useState<PendingAction | null>(null)
  const [focused, setFocused] = useState<boolean | undefined>()
  const [value, setValue] = useState('')
  const [flags, setFlags] = useState<RunFlags>({})
  const [toast, setToast] = useState({ message: '', error: false })
  const mutation = useRowMutation()
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  // The preview of a run action comes from the server (the ONE place the
  // argv is decided); fetched while the dialog is open, re-fetched when a
  // flag changes, so the preview is always the command that will run.
  const runKind = pending && isRunKind(pending.kind) ? pending.kind : ''
  const plan = useRunPlan(
    runKind ? pending!.subject.project : '',
    runKind ? (pending!.subject.task_id ?? '') : '',
    runKind,
    flags,
  )

  useEffect(() => () => clearTimeout(timer.current), [])
  const notify = (message: string, error: boolean) => {
    setToast({ message, error })
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setToast({ message: '', error: false }), TOAST_MS)
  }

  const ask = useCallback((p: PendingAction, f?: boolean) => {
    mutation.reset()
    setPending(p)
    setFocused(f)
    setValue(initialValue(p))
    setFlags({})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const cancel = () => setPending(null)
  const confirm = () => {
    if (!pending) return
    if (runKind && plan.data === undefined) return // the preview is the contract; wait for it
    mutation.mutate(requestFor(pending, value, flags), {
      onSuccess: (res) => {
        setPending(null)
        notify(runKind ? runToast(runKind, res as Record<string, unknown>) : 'saved', false)
      },
      onError: (e) => notify(e.message, true),
    })
  }

  let text: ConfirmText | null = null
  if (pending) {
    text = describeAction(pending, value, focused, runKind ? plan.data : undefined)
    // A plan that failed to load is no plan: the preview IS the contract, so
    // the confirm button stays disabled (loading) and the error says why -
    // never an enabled button whose click does nothing.
    if (runKind && plan.isError)
      text = {
        ...text,
        loading: true,
        preview: '(no command - the plan could not be loaded)',
        warnings: [`plan: ${plan.error.message}`],
      }
  }
  return {
    pending,
    text,
    value,
    setValue,
    ask,
    confirm,
    cancel,
    busy: mutation.isPending,
    error: mutation.error?.message,
    toast,
    flags,
    setFlags,
  }
}

/** What the toast says after a run action: the pid and the log, the claim, the kill. */
function runToast(kind: string, res: Record<string, unknown>): string {
  switch (kind) {
    case 'rerun_finish':
    case 'resume_run': {
      const r = res as unknown as RunStarted
      return `started ${r.kind} (pid ${r.pid}) · log ${r.log}`
    }
    case 'claim':
      return res.refreshed
        ? 'claim refreshed'
        : 'claimed - pm serve keeps it alive for up to an hour'
    case 'release_claim':
      return res.released ? 'claim released' : 'no claim to release'
    case 'kill':
      return `stopped pid ${String(res.pid)}${res.parked ? ` · parked ${String(res.parked)} on waiting` : ''}`
    default:
      return 'done'
  }
}
