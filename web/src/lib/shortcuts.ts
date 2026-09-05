// Keyboard map: one table drives both the listener and the "?" help dialog,
// so the list on screen can never disagree with what the keys do.

export type Action =
  | 'down'
  | 'up'
  | 'open'
  | 'close'
  | 'help'
  | 'palette'
  | 'today'
  | 'changes'
  | 'runs'
  | 'settings'
  | 'focus'
  | 'groupPrefix'

export interface Shortcut {
  keys: string
  action: Action
  description: string
}

export const SHORTCUTS: readonly Shortcut[] = [
  { keys: '1', action: 'today', description: 'Today (home)' },
  { keys: '2', action: 'changes', description: 'Changes' },
  { keys: '3', action: 'runs', description: 'Runs' },
  { keys: ',', action: 'settings', description: 'Settings' },
  { keys: 'j', action: 'down', description: 'next row' },
  { keys: 'k', action: 'up', description: 'previous row' },
  { keys: 'Enter', action: 'open', description: 'open the selected row' },
  { keys: 't', action: 'focus', description: 'toggle focus on the selected row' },
  {
    keys: 'g <letter>',
    action: 'groupPrefix',
    description: 'filter home by group (g, then its letter)',
  },
  { keys: 'Esc', action: 'close', description: 'close the task / dialog' },
  { keys: '?', action: 'help', description: 'this list' },
  { keys: '⌘K / Ctrl+K', action: 'palette', description: 'command palette' },
]

/** The minimal slice of KeyboardEvent the mapping reads (tests build it by hand). */
export interface KeyLike {
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  altKey?: boolean
  target?: EventTarget | null
}

/** True when keystrokes belong to the element - an input, a textarea, anything editable. */
export function isTypingTarget(el: EventTarget | null | undefined): boolean {
  if (!el || !(el instanceof Element)) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  // jsdom leaves isContentEditable undefined, so read the attribute too.
  return (
    el instanceof HTMLElement &&
    (el.isContentEditable === true || el.getAttribute('contenteditable') === 'true')
  )
}

/**
 * Maps a keystroke to an action, or null. The palette chord works everywhere
 * (it is how you leave a text field); every other key is ignored while typing
 * and while a modifier is held, so browser shortcuts stay browser shortcuts.
 * `g` only opens the group chord - the hook waits for the letter.
 */
export function shortcutFor(e: KeyLike): Action | null {
  const k = e.key
  if ((e.metaKey || e.ctrlKey) && (k === 'k' || k === 'K')) return 'palette'
  if (e.metaKey || e.ctrlKey || e.altKey) return null
  if (k === 'Escape') return 'close'
  if (isTypingTarget(e.target)) return null
  switch (k) {
    case 'j':
      return 'down'
    case 'k':
      return 'up'
    case 'Enter':
      return 'open'
    case '?':
      return 'help'
    case '1':
      return 'today'
    case '2':
      return 'changes'
    case '3':
      return 'runs'
    case ',':
      return 'settings'
    case 't':
      return 'focus'
    case 'g':
      return 'groupPrefix'
    default:
      return null
  }
}
