import type { Project, TimelineEntry, TimelineRead } from '../api/types'
import { parseStamp } from './relativeTime'

// "Where we left off", per repo: the timeline's latest state and what
// happened after it when the repo keeps a timeline, its hand-written notes
// when it does not. Which state and which entries is the API's read; this
// decides what a repo shows and words it.

export interface TimelineRef {
  label: string
  /** Set when the ref is a URL. */
  href?: string
}

export interface TimelineLine {
  id: string
  /** The local calendar date, YYYY-MM-DD. */
  date: string
  kind: string
  text: string
  refs: TimelineRef[]
}

export type LeftOffView =
  | {
      kind: 'timeline'
      state: TimelineLine | null
      /** Oldest first, as the API reads them. */
      entries: TimelineLine[]
      /** What the entry list is ("2 entries since this state"). */
      heading: string
      /** '' unless the state is stale. */
      stale: string
    }
  | { kind: 'notes'; notes: string }

const URL_REF = /^https?:\/\//

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** The local date of a stamp; an unparsable stamp is shown as it is. */
export function dateOf(ts: string): string {
  const d = parseStamp(ts)
  if (d === null) return ts
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export function lineOf(e: TimelineEntry): TimelineLine {
  return {
    id: e.id,
    date: dateOf(e.ts),
    kind: e.kind,
    text: e.text,
    refs: (e.refs ?? []).map((r) => (URL_REF.test(r) ? { label: r, href: r } : { label: r })),
  }
}

/** The stale line: how old the state is and how much happened since. '' when fresh. */
export function staleLine(read: TimelineRead): string {
  if (!read.stale) return ''
  return `this state is ${plural(read.days_since, 'day', 'days')} old with ${plural(read.entries_since, 'entry', 'entries')} after it - time to write a new one`
}

export function entriesHeading(read: TimelineRead): string {
  if (read.state === null) {
    return `no state yet · the newest ${read.since.length} of ${plural(read.total, 'entry', 'entries')}`
  }
  return read.since.length === 0
    ? 'nothing since this state'
    : `${plural(read.since.length, 'entry', 'entries')} since this state`
}

/**
 * What a repo shows: its timeline when it has one, else its notes. No read
 * (still loading, or the request failed) and an empty timeline both fall
 * back to the notes, so the section is never blank for a repo that has not
 * started a timeline.
 */
export function leftOffView(project: Project, read: TimelineRead | undefined): LeftOffView {
  if (read === undefined || read.total === 0) return { kind: 'notes', notes: project.notes ?? '' }
  return {
    kind: 'timeline',
    state: read.state === null ? null : lineOf(read.state),
    entries: read.since.map(lineOf),
    heading: entriesHeading(read),
    stale: staleLine(read),
  }
}
