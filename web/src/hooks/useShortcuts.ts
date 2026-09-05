import { useEffect, useRef } from 'react'

import { isGroupLetter } from '../lib/groupFilter'
import { shortcutFor, type Action } from '../lib/shortcuts'

// The window keydown listener. The MAPPING is lib/shortcuts (tested); this is
// only the wiring, plus two rules of its own: while a modal <dialog> is open
// it owns the keyboard (cmdk handles arrows/Enter, Esc closes natively), so
// only the palette chord - a toggle - passes through; and `g` arms a chord
// whose next letter is handed to the `group` handler (or dropped after
// CHORD_MS, so a stray `g` does not swallow the next keystroke forever).

export type ShortcutHandlers = Partial<Record<Exclude<Action, 'groupPrefix'>, () => void>> & {
  /** The `g <letter>` chord: receives the letter. */
  group?: (letter: string) => void
}

const CHORD_MS = 1500

export function useShortcuts(handlers: ShortcutHandlers) {
  const ref = useRef(handlers)
  useEffect(() => {
    ref.current = handlers
  })
  useEffect(() => {
    let armedAt = 0
    const onKey = (e: KeyboardEvent) => {
      const armed = armedAt > 0 && Date.now() - armedAt < CHORD_MS
      armedAt = 0
      if (armed && ref.current.group && isGroupLetter(e.key) && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        ref.current.group(e.key)
        return
      }
      const action = shortcutFor(e)
      if (action === null) return
      if (action !== 'palette' && document.querySelector('dialog[open]')) return
      if (action === 'groupPrefix') {
        if (ref.current.group) {
          armedAt = Date.now()
          e.preventDefault()
        }
        return
      }
      const fn = ref.current[action]
      if (!fn) return
      e.preventDefault()
      fn()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}
