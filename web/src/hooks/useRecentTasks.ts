import { useCallback, useSyncExternalStore } from 'react'

import type { RecentTask } from '../lib/paletteItems'
import { pushRecent } from '../lib/recent'

// Recently opened tasks, kept in localStorage so the palette's empty state
// survives a reload. The list rule is lib/recent; this is storage + React.

const KEY = 'pm.recent'
const listeners = new Set<() => void>()
let cache: RecentTask[] | undefined

function read(): RecentTask[] {
  if (cache) return cache
  try {
    cache = JSON.parse(localStorage.getItem(KEY) ?? '[]') as RecentTask[]
  } catch {
    cache = []
  }
  return cache
}

function write(list: RecentTask[]) {
  cache = list
  try {
    localStorage.setItem(KEY, JSON.stringify(list))
  } catch {
    // Storage full or disabled: the in-memory list still works this session.
  }
  for (const fn of listeners) fn()
}

function subscribe(fn: () => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function useRecentTasks() {
  const recent = useSyncExternalStore(subscribe, read, read)
  const push = useCallback((item: RecentTask) => write(pushRecent(read(), item)), [])
  return { recent, push }
}
