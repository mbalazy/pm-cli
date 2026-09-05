import { Command } from 'cmdk'
import { Search } from 'lucide-react'
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
      className="dialog-in mx-auto mt-[12vh] w-[min(40rem,92vw)] rounded-md border border-rule-strong bg-paper p-0 text-ink shadow-2xl"
    >
      <Command shouldFilter={false} label="Command palette">
        <div className="flex items-center gap-2 border-b border-rule px-3">
          <Search aria-hidden="true" className="size-4 shrink-0 text-ink-3" strokeWidth={1.75} />
          <Command.Input
            value={query}
            onValueChange={onQueryChange}
            placeholder="project, task id or title, runs…"
            className="w-full bg-transparent py-3 text-[0.9375rem] outline-none placeholder:text-ink-3"
          />
          <kbd className="text-xs text-ink-3">esc</kbd>
        </div>
        <Command.List className="max-h-80 overflow-y-auto p-1">
          <Command.Empty className="p-3 text-sm text-ink-3">nothing matches</Command.Empty>
          {items.map((item) => (
            <Command.Item
              key={`${item.kind}:${item.id}`}
              value={`${item.kind}:${item.id}`}
              onSelect={() => onSelect(item)}
              className="cursor-pointer rounded-sm px-3 py-1.5 text-sm data-[selected=true]:bg-highlight data-[selected=true]:text-highlight-ink"
            >
              {item.label}
            </Command.Item>
          ))}
        </Command.List>
      </Command>
    </dialog>
  )
}
