// Task stamps come in two shapes and pm never migrates files: a bare
// YYYY-MM-DD (older `updated`, every `created`) or a full RFC3339 stamp.
// Both are read in the reader's local zone, like the TUI's relativeTime.

const BARE_DATE = /^\d{4}-\d{2}-\d{2}$/

/** Parses a pm stamp; a bare date is local midnight. null when unparsable. */
export function parseStamp(stamp: string): Date | null {
  if (BARE_DATE.test(stamp)) {
    const [y, m, d] = stamp.split('-').map(Number)
    return new Date(y, m - 1, d)
  }
  const t = new Date(stamp)
  return Number.isNaN(t.getTime()) ? null : t
}

/** Whole local calendar days between `at` and `now` (0 = same day). */
export function calendarDaysAgo(at: Date, now: Date): number {
  const day = (d: Date) => Date.UTC(d.getFullYear(), d.getMonth(), d.getDate())
  return Math.round((day(now) - day(at)) / 86_400_000)
}

/**
 * "just now" / "12m ago" / "3h ago" within today for a full stamp, "today"
 * for a bare date, then calendar days ("1d ago", "5d ago") - days, not
 * hours, once the day boundary is crossed, like the TUI. An empty stamp is
 * UNKNOWN and renders empty (status_changed on an older task); an
 * unparsable one is shown as it is rather than guessed at.
 */
export function relativeTime(stamp: string, now: Date = new Date()): string {
  if (stamp === '') return ''
  const at = parseStamp(stamp)
  if (at === null) return stamp
  const days = calendarDaysAgo(at, now)
  if (days > 0) return `${days}d ago`
  if (days < 0) return 'in the future'
  if (BARE_DATE.test(stamp)) return 'today'
  const minutes = Math.floor((now.getTime() - at.getTime()) / 60_000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  return `${Math.floor(minutes / 60)}h ago`
}
