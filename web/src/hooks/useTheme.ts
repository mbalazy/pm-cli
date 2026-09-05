import { useEffect, useState } from 'react'

// The colour theme: system by default, light or dark by choice, remembered in
// localStorage (UI state - never a server setting). Applies the `dark` class
// on <html>, which is what the stylesheet's dark tokens and Tailwind's dark:
// variant key on. React glue only; no domain rule.

export type ThemeChoice = 'system' | 'light' | 'dark'
export const THEME_CHOICES: readonly ThemeChoice[] = ['system', 'light', 'dark']

const KEY = 'pm.theme'

function readChoice(): ThemeChoice {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // no storage (private mode, tests) - system it is
  }
  return 'system'
}

function prefersDark(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches
}

export function useTheme() {
  const [choice, setChoice] = useState<ThemeChoice>(readChoice)

  useEffect(() => {
    const apply = () => {
      const dark = choice === 'dark' || (choice === 'system' && prefersDark())
      document.documentElement.classList.toggle('dark', dark)
    }
    apply()
    if (choice !== 'system' || typeof matchMedia !== 'function') return
    const mq = matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener?.('change', apply)
    return () => mq.removeEventListener?.('change', apply)
  }, [choice])

  const set = (next: ThemeChoice) => {
    setChoice(next)
    try {
      if (next === 'system') localStorage.removeItem(KEY)
      else localStorage.setItem(KEY, next)
    } catch {
      // storage unavailable - the choice still holds for this page
    }
  }
  /** Steps system -> light -> dark -> system. */
  const cycle = () => set(THEME_CHOICES[(THEME_CHOICES.indexOf(choice) + 1) % THEME_CHOICES.length])

  return { choice, set, cycle }
}
