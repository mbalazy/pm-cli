import type { RunRow } from '../api/types'

/**
 * The live table is the local rows (refetched on every `runs` event) plus the
 * remote rows of the last explicit remote fetch - which comes back as local +
 * remote sorted together, so only its remote-tagged rows are taken. Order
 * within each part is the API's; the parts are not re-sorted against each
 * other (the fetches happened at different times, a merged order would lie).
 */
export function mergeRunRows(local: RunRow[], remoteFetch: RunRow[] | undefined): RunRow[] {
  if (!remoteFetch) return local
  return [...local, ...remoteFetch.filter((r) => Boolean(r.remote))]
}
