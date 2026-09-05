import type { SourceStatus } from '../api/types'
import { clockLabel } from './ageLabel'

// The header's "refreshed HH:MM · next HH:MM": the newest source fetch and
// that plus the configured interval - moved into the refresh window when it
// would fall outside it (the scheduler never fetches outside the window, so
// "next 22:07" with a 07:00-20:00 window was a promise nobody kept - ux-audit
// F-15). Everything comes from the API (the feed's source statuses, the
// config's refresh.every_seconds and refresh.window); this only formats.

export interface RefreshTimes {
  refreshed: string
  next: string
}

/** "HH:MM-HH:MM" -> minutes since midnight, or null when unparsable. */
export function parseWindow(window: string | undefined): { start: number; end: number } | null {
  const m = /^(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})$/.exec(window?.trim() ?? '')
  if (!m) return null
  const start = Number(m[1]) * 60 + Number(m[2])
  const end = Number(m[3]) * 60 + Number(m[4])
  if (start >= end || end > 24 * 60) return null
  return { start, end }
}

/** The first instant at or after `at` that lies inside the window (local time). */
export function nextInWindow(at: Date, window: string | undefined): Date {
  const w = parseWindow(window)
  if (!w) return at
  const minutes = at.getHours() * 60 + at.getMinutes()
  if (minutes >= w.start && minutes < w.end) return at
  const next = new Date(at)
  next.setSeconds(0, 0)
  next.setHours(Math.floor(w.start / 60), w.start % 60)
  if (minutes >= w.end) next.setDate(next.getDate() + 1)
  return next
}

export function refreshTimes(
  sources: SourceStatus[] | undefined,
  everySeconds: number | undefined,
  window?: string,
): RefreshTimes {
  let last = ''
  for (const s of sources ?? []) {
    if (s.last_fetch && s.last_fetch > last) last = s.last_fetch
  }
  if (last === '') return { refreshed: '', next: '' }
  const at = new Date(last)
  if (Number.isNaN(at.getTime())) return { refreshed: '', next: '' }
  const next =
    everySeconds && everySeconds > 0
      ? clockLabel(nextInWindow(new Date(at.getTime() + everySeconds * 1000), window).toISOString())
      : ''
  return { refreshed: clockLabel(last), next }
}
