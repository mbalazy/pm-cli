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
      className="m-auto rounded border p-4 shadow-lg backdrop:bg-black/30"
    >
      <h2 className="mb-2 font-semibold">Keyboard shortcuts</h2>
      <table className="text-sm">
        <tbody>
          {SHORTCUTS.map((s) => (
            <tr key={s.action}>
              <td className="pr-4">
                <kbd className="rounded border px-1">{s.keys}</kbd>
              </td>
              <td>{s.description}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" onClick={onClose} className="mt-3 text-sm underline">
        close
      </button>
    </dialog>
  )
}
