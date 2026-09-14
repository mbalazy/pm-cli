import type { ChangeEvent, SourceStatus } from '../api/types'
import { clockLabel } from './ageLabel'
import { calendarDaysAgo, parseStamp } from './relativeTime'

// The Changes screen's rules: which events the filters keep, what the source
// chips say, how a stamp reads next to a row. The events, their order and
// the source statuses are the API's (/api/changes); nothing here fetches or
// re-sorts.

export interface ChangesFilter {
  /** Sources kept; empty = every source. */
  sources: ReadonlySet<string>
  /** One group, '' = every group. */
  group: string
}

export function filterEvents(events: ChangeEvent[], f: ChangesFilter): ChangeEvent[] {
  return events.filter(
    (e) =>
      (f.sources.size === 0 || f.sources.has(e.source)) && (f.group === '' || e.group === f.group),
  )
}

/**
 * The raw feed's dismiss view: seen (dismissed) events leave the list unless
 * the user asks to see them again.
 */
export function visibleEvents(events: ChangeEvent[], showSeen: boolean): ChangeEvent[] {
  return showSeen ? events : events.filter((e) => !e.seen)
}

/** Events per source, for the chip counts (before the source filter, after the group filter). */
export function sourceCounts(events: ChangeEvent[]): Map<string, number> {
  const m = new Map<string, number>()
  for (const e of events) m.set(e.source, (m.get(e.source) ?? 0) + 1)
  return m
}

/** The distinct groups the events name, in first-seen order (the group filter's chips). */
export function eventGroups(events: ChangeEvent[]): string[] {
  const out: string[] = []
  for (const e of events) if (e.group && !out.includes(e.group)) out.push(e.group)
  return out
}

/** Toggles one source in the set; an empty set means "all", so toggling off the last one clears it. */
export function toggleSource(set: ReadonlySet<string>, source: string, all: string[]): Set<string> {
  const next = new Set(set.size === 0 ? all : set)
  if (next.has(source)) next.delete(source)
  else next.add(source)
  return next.size === all.length ? new Set() : next
}

/**
 * The row's time: "HH:MM" today, "yest. HH:MM" yesterday, "Mon DD HH:MM"
 * before that - the wireframe's rule, in the reader's zone.
 */
export function eventTimeText(ts: string, now: Date = new Date()): string {
  const at = parseStamp(ts)
  if (at === null) return ts
  const days = calendarDaysAgo(at, now)
  const hhmm = clockLabel(ts)
  if (days <= 0) return hhmm
  if (days === 1) return `yest. ${hhmm}`
  return `${at.toLocaleDateString(undefined, { month: 'short', day: '2-digit' })} ${hhmm}`
}

export type SourceState = 'off' | 'never' | 'error' | 'ok'

export interface SourceChip {
  name: string
  state: SourceState
  /** "last 10:12", "error: gh: not logged in", "off", "never fetched". */
  text: string
  events: number
}

/** One chip per source status (the registry's order is the API's). */
export function sourceChips(
  sources: SourceStatus[] | undefined,
  now: Date = new Date(),
): SourceChip[] {
  return (sources ?? []).map((s) => {
    if (!s.enabled) return { name: s.name, state: 'off', text: 'off', events: s.events }
    if (s.error)
      return { name: s.name, state: 'error', text: `error: ${s.error}`, events: s.events }
    if (!s.last_fetch)
      return { name: s.name, state: 'never', text: 'never fetched', events: s.events }
    return {
      name: s.name,
      state: 'ok',
      text: `last ${eventTimeText(s.last_fetch, now)}`,
      events: s.events,
    }
  })
}

/** "since yesterday 18:00" / "since today 18:00" off the API's cutoff stamp. */
export function cutoffText(cutoff: string, now: Date = new Date()): string {
  const at = parseStamp(cutoff)
  if (at === null) return cutoff ? `since ${cutoff}` : ''
  const days = calendarDaysAgo(at, now)
  const hhmm = clockLabel(cutoff)
  if (days <= 0) return `since today ${hhmm}`
  if (days === 1) return `since yesterday ${hhmm}`
  return `since ${eventTimeText(cutoff, now)}`
}
