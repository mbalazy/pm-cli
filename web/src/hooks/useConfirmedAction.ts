import { useCallback, useEffect, useRef, useState } from 'react'

import { useRowMutation } from '../api/mutations'
import {
  describeAction,
  initialValue,
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
}

const TOAST_MS = 3000

export function useConfirmedAction(): ConfirmedAction {
  const [pending, setPending] = useState<PendingAction | null>(null)
  const [focused, setFocused] = useState<boolean | undefined>()
  const [value, setValue] = useState('')
  const [toast, setToast] = useState({ message: '', error: false })
  const mutation = useRowMutation()
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const cancel = () => setPending(null)
  const confirm = () => {
    if (!pending) return
    mutation.mutate(requestFor(pending, value), {
      onSuccess: () => {
        setPending(null)
        notify('saved', false)
      },
      onError: (e) => notify(e.message, true),
    })
  }

  return {
    pending,
    text: pending ? describeAction(pending, value, focused) : null,
    value,
    setValue,
    ask,
    confirm,
    cancel,
    busy: mutation.isPending,
    error: mutation.error?.message,
    toast,
  }
}
