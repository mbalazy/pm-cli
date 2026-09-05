import { useState } from 'react'

// Whether the sidebar is folded to its rail. UI state remembered per
// browser (localStorage), never a server setting - the sidebar's VARIANT
// and width are config (cockpit.sidebar); this only folds it for now.

const KEY = 'pm.sidebar.collapsed'

export function useSidebarCollapsed() {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(KEY) === '1'
    } catch {
      return false
    }
  })
  const set = (v: boolean) => {
    setCollapsed(v)
    try {
      if (v) localStorage.setItem(KEY, '1')
      else localStorage.removeItem(KEY)
    } catch {
      // no storage - the fold still holds for this page
    }
  }
  return { collapsed, toggle: () => set(!collapsed) }
}
