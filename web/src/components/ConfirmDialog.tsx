import { TriangleAlert } from 'lucide-react'
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
      // reaches the dialog (a real showModal moves focus too; jsdom's shim does
      // not). A disabled confirm button (the plan still loading) cannot take
      // focus, so the dialog itself does: Esc still lands here, never on the
      // control behind the dialog that opened it.
      ;(
        fieldRef.current ??
        d.querySelector<HTMLElement>('button[type=submit]:not(:disabled)') ??
        d
      ).focus()
    }
    if (!open && d.open) d.close()
  }, [open])
  // Once the plan arrives, move from the dialog itself to the confirm button,
  // so Enter confirms as promised in the footer.
  const loading = text?.loading === true
  useEffect(() => {
    const d = ref.current
    if (!d || !open || loading || document.activeElement !== d) return
    d.querySelector<HTMLElement>('button[type=submit]:not(:disabled)')?.focus()
  }, [open, loading])

  return (
    <dialog
      ref={ref}
      aria-label="Confirm"
      tabIndex={-1}
      onClose={onCancel}
      onKeyDown={(e) => {
        // A real <dialog> cancels on Esc by itself; jsdom's does not.
        if (e.key === 'Escape') onCancel()
      }}
      className="dialog-in m-auto w-[min(34rem,92vw)] rounded-md border border-rule-strong bg-paper p-0 text-ink shadow-2xl"
    >
      {text && (
        <form
          method="dialog"
          onSubmit={(e) => {
            e.preventDefault()
            if (!busy) onConfirm()
          }}
          className="space-y-3 p-5 text-sm"
        >
          <header className="space-y-1 border-b border-rule pb-3">
            <h2 className="display text-lg leading-snug">{text.heading}</h2>
            {text.project && (
              <p>
                <span className="chip">{text.project}</span>
              </p>
            )}
          </header>
          <p className="text-[0.9375rem]">{text.sentence}</p>
          {text.field && (
            <label className="block">
              <span className="kicker">{text.field.label}</span>
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
                  rows={7}
                  className="field mt-1 w-full font-mono text-xs leading-relaxed"
                />
              ) : (
                <input
                  ref={fieldRef as React.RefObject<HTMLInputElement>}
                  value={value}
                  onChange={(e) => onChange(e.target.value)}
                  placeholder={text.field.placeholder}
                  className="field mt-1 w-full"
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
                    className="accent-ink"
                    checked={flags?.[f.key] === true}
                    disabled={f.disabled !== undefined}
                    onChange={(e) => onFlagsChange?.({ ...flags, [f.key]: e.target.checked })}
                  />
                  {f.label}
                  {f.disabled && <span className="text-xs text-ink-3">({f.disabled})</span>}
                </label>
              ))}
            </fieldset>
          )}
          {(text.preview !== undefined || text.loading) && (
            <pre
              aria-label="command preview"
              className="overflow-x-auto rounded-sm bg-paper-2 p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink"
            >
              {text.loading ? 'loading the preview…' : text.preview || '(no command)'}
            </pre>
          )}
          {text.warnings?.map((w) => (
            <p key={w} role="alert" className="flex items-start gap-1.5 text-warn">
              <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
              {w}
            </p>
          ))}
          {text.warning && (
            <p role="alert" className="flex items-start gap-1.5 text-warn">
              <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
              {text.warning}
            </p>
          )}
          {text.blocked && (
            <p role="alert" className="text-crit">
              start is disabled: {text.blocked}
            </p>
          )}
          {error && <p className="text-crit">error: {error}</p>}
          <div className="flex items-center gap-2 border-t border-rule pt-3">
            <button
              type="submit"
              disabled={busy || text.loading || text.blocked !== undefined}
              className="rounded-sm bg-ink px-3 py-1 font-medium text-paper transition-opacity hover:opacity-90 disabled:opacity-50"
            >
              {busy ? 'working…' : text.confirmLabel}
            </button>
            <button type="button" onClick={onCancel} className="ghost-btn py-0.5 text-sm">
              cancel
            </button>
            <span className="ml-auto text-xs text-ink-3">
              {text.field?.kind === 'textarea' ? '⌘⏎ confirm' : '⏎ confirm'} · Esc cancel
            </span>
          </div>
        </form>
      )}
    </dialog>
  )
}
