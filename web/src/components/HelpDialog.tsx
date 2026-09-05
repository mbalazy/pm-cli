import { useEffect, useRef } from 'react'

import { SHORTCUTS } from '../lib/shortcuts'

export function HelpDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal()
    if (!open && d.open) d.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      aria-label="Keyboard shortcuts"
      onClose={onClose}
      className="dialog-in m-auto w-[min(26rem,92vw)] rounded-md border border-rule-strong bg-paper p-5 text-ink shadow-2xl"
    >
      <h2 className="display mb-3 border-b border-rule pb-2 text-lg">Keyboard shortcuts</h2>
      <table className="w-full text-sm">
        <tbody>
          {SHORTCUTS.map((s) => (
            <tr key={s.action}>
              <td className="py-0.5 pr-4 align-top whitespace-nowrap">
                <kbd className="rounded-sm border border-rule bg-paper-2 px-1.5 text-xs text-ink-2">
                  {s.keys}
                </kbd>
              </td>
              <td className="py-0.5 text-ink-2">{s.description}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" onClick={onClose} className="ghost-btn mt-4">
        close
      </button>
    </dialog>
  )
}
