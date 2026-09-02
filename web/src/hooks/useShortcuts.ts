import { useEffect, useRef } from 'react'

import { shortcutFor, type Action } from '../lib/shortcuts'

// The window keydown listener. The MAPPING is lib/shortcuts (tested); this is
// only the wiring, plus one rule of its own: while a modal <dialog> is open
// it owns the keyboard (cmdk handles arrows/Enter, Esc closes natively), so
// only the palette chord - a toggle - passes through.

export type ShortcutHandlers = Partial<Record<Action, () => void>>

export function useShortcuts(handlers: ShortcutHandlers) {
  const ref = useRef(handlers)
  useEffect(() => {
    ref.current = handlers
  })
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const action = shortcutFor(e)
      if (action === null) return
      if (action !== 'palette' && document.querySelector('dialog[open]')) return
      const fn = ref.current[action]
      if (!fn) return
      e.preventDefault()
      fn()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}
