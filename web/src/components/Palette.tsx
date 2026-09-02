import { Command } from 'cmdk'
import { useEffect, useRef } from 'react'

import type { PaletteItem } from '../lib/paletteItems'

// ⌘K palette: cmdk inside a native <dialog>. Filtering is OFF here - the
// items arrive already matched (lib/paletteItems) - so this is list-in,
// choice-out.

interface Props {
  open: boolean
  query: string
  items: PaletteItem[]
  onQueryChange: (q: string) => void
  onSelect: (item: PaletteItem) => void
  onClose: () => void
}

export function Palette({ open, query, items, onQueryChange, onSelect, onClose }: Props) {
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
      aria-label="Command palette"
      onClose={onClose}
      className="m-auto w-[min(40rem,90vw)] rounded border p-0 shadow-lg backdrop:bg-black/30"
    >
      <Command shouldFilter={false} label="Command palette">
        <Command.Input
          value={query}
          onValueChange={onQueryChange}
          placeholder="project, task id or title, runs…"
          className="w-full border-b p-3 outline-none"
        />
        <Command.List className="max-h-80 overflow-y-auto p-1">
          <Command.Empty className="p-3 text-sm text-gray-500">nothing matches</Command.Empty>
          {items.map((item) => (
            <Command.Item
              key={`${item.kind}:${item.id}`}
              value={`${item.kind}:${item.id}`}
              onSelect={() => onSelect(item)}
              className="cursor-pointer rounded px-3 py-1 data-[selected=true]:bg-yellow-100"
            >
              {item.label}
            </Command.Item>
          ))}
        </Command.List>
      </Command>
    </dialog>
  )
}
