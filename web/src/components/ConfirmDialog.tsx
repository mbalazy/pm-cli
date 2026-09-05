import { useEffect, useRef } from 'react'

import type { RunFlags } from '../api/types'
import type { ConfirmText } from '../lib/confirmText'

// The one confirmation dialog every mutation passes through. What it says is
// lib/confirmText's; this renders it: heading (id + title), project, the
// sentence, an optional field (pre-filled), a warning, confirm/cancel. Enter
// submits the form (an input's natural Enter; a textarea takes ⌘/Ctrl+Enter
// so a newline stays a newline), Esc closes the native <dialog>.

interface Props {
  open: boolean
  text: ConfirmText | null
  value: string
  onChange: (v: string) => void
  onConfirm: () => void
  onCancel: () => void
  busy?: boolean
  error?: string
  /** A run action's flags and their setter (the checkboxes); optional elsewhere. */
  flags?: RunFlags
  onFlagsChange?: (f: RunFlags) => void
}

export function ConfirmDialog({
  open,
  text,
  value,
  onChange,
  onConfirm,
  onCancel,
  busy,
  error,
  flags,
  onFlagsChange,
}: Props) {
  const ref = useRef<HTMLDialogElement>(null)
  const fieldRef = useRef<HTMLInputElement | HTMLTextAreaElement>(null)
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) {
      d.showModal()
      // Focus the field, else the confirm button - so Enter confirms and Esc
      // reaches the dialog (a real showModal moves focus too; jsdom's shim does not).
      ;(fieldRef.current ?? d.querySelector<HTMLElement>('button[type=submit]'))?.focus()
    }
    if (!open && d.open) d.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      aria-label="Confirm"
      onClose={onCancel}
      onKeyDown={(e) => {
        // A real <dialog> cancels on Esc by itself; jsdom's does not.
        if (e.key === 'Escape') onCancel()
      }}
      className="m-auto w-[min(32rem,90vw)] rounded border p-4 shadow-lg backdrop:bg-black/30"
    >
      {text && (
        <form
          method="dialog"
          onSubmit={(e) => {
            e.preventDefault()
            if (!busy) onConfirm()
          }}
          className="space-y-3 text-sm"
        >
          <h2 className="font-semibold">{text.heading}</h2>
          {text.project && (
            <p className="text-gray-600">
              <span className="rounded bg-gray-100 px-1 text-xs">{text.project}</span>
            </p>
          )}
          <p>{text.sentence}</p>
          {text.field && (
            <label className="block">
              <span className="text-xs text-gray-500">{text.field.label}</span>
              {text.field.kind === 'textarea' ? (
                <textarea
                  ref={fieldRef as React.RefObject<HTMLTextAreaElement>}
                  value={value}
                  onChange={(e) => onChange(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault()
                      if (!busy) onConfirm()
                    }
                  }}
                  placeholder={text.field.placeholder}
                  rows={6}
                  className="mt-1 w-full rounded border p-2 font-mono text-xs"
                />
              ) : (
                <input
                  ref={fieldRef as React.RefObject<HTMLInputElement>}
                  value={value}
                  onChange={(e) => onChange(e.target.value)}
                  placeholder={text.field.placeholder}
                  className="mt-1 w-full rounded border p-1"
                />
              )}
            </label>
          )}
          {text.flags && text.flags.length > 0 && (
            <fieldset className="space-y-1">
              {text.flags.map((f) => (
                <label key={f.key} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={flags?.[f.key] === true}
                    disabled={f.disabled !== undefined}
                    onChange={(e) => onFlagsChange?.({ ...flags, [f.key]: e.target.checked })}
                  />
                  {f.label}
                  {f.disabled && <span className="text-xs text-gray-400">({f.disabled})</span>}
                </label>
              ))}
            </fieldset>
          )}
          {(text.preview !== undefined || text.loading) && (
            <pre
              aria-label="command preview"
              className="overflow-x-auto rounded bg-gray-100 p-2 font-mono text-xs whitespace-pre-wrap"
            >
              {text.loading ? 'loading the preview…' : text.preview || '(no command)'}
            </pre>
          )}
          {text.warnings?.map((w) => (
            <p key={w} role="alert" className="text-amber-700">
              ⚠ {w}
            </p>
          ))}
          {text.warning && (
            <p role="alert" className="text-amber-700">
              ⚠ {text.warning}
            </p>
          )}
          {error && <p className="text-red-700">error: {error}</p>}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={busy || text.loading}
              className="rounded border px-2 font-semibold disabled:text-gray-400"
            >
              {busy ? 'working…' : text.confirmLabel}
            </button>
            <button type="button" onClick={onCancel} className="rounded border px-2">
              cancel
            </button>
            <span className="ml-auto text-xs text-gray-400">
              {text.field?.kind === 'textarea' ? '⌘⏎ confirm' : '⏎ confirm'} · Esc cancel
            </span>
          </div>
        </form>
      )}
    </dialog>
  )
}
