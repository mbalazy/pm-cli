import type { SourceStatus } from '../api/types'
import { clockLabel } from './ageLabel'

// The header's "refreshed HH:MM · next HH:MM": the newest source fetch and
// that plus the configured interval. Both come from the API (the feed's
// source statuses, the config's refresh.every_seconds); this only formats.

export interface RefreshTimes {
  refreshed: string
  next: string
}

export function refreshTimes(
  sources: SourceStatus[] | undefined,
  everySeconds: number | undefined,
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
      ? clockLabel(new Date(at.getTime() + everySeconds * 1000).toISOString())
      : ''
  return { refreshed: clockLabel(last), next }
}
