// An attention row's age, off `age_seconds` ONLY. null is UNKNOWN (the task
// predates the status_changed stamp) and says so - never a guess off another
// stamp, that is the aggregation's rule and this is where it is displayed.

export function ageLabel(ageSeconds: number | null | undefined): string {
  if (ageSeconds === null || ageSeconds === undefined) return 'since ?'
  if (ageSeconds < 0) return 'in the future'
  const minutes = Math.floor(ageSeconds / 60)
  if (minutes < 1) return '<1m'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

/** HH:MM in the reader's local zone; empty for an unparsable stamp. */
export function clockLabel(stamp: string | undefined): string {
  if (!stamp) return ''
  const d = new Date(stamp)
  if (Number.isNaN(d.getTime())) return ''
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}
