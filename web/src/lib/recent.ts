import type { RecentTask } from './paletteItems'

export const RECENT_CAP = 10

/** Moves `item` to the front of the recent list, deduplicated, capped. Pure. */
export function pushRecent(list: RecentTask[], item: RecentTask, cap = RECENT_CAP): RecentTask[] {
  const rest = list.filter((r) => !(r.project === item.project && r.id === item.id))
  return [item, ...rest].slice(0, cap)
}
