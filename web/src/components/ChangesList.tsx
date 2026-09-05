import type { ChangeEvent } from '../api/types'
import { EventRow } from './EventRow'
import { SectionHead } from './SectionHead'

// The change feed as a plain list (the group tab's view; the full Changes
// screen is routes/ChangesPage). Events arrive filtered and ordered.

interface Props {
  events: ChangeEvent[]
  cutoff: string
  /** Formats an event's stamp - a clock from the page. */
  timeText: (ts: string) => string
}

export function ChangesList({ events, cutoff, timeText }: Props) {
  return (
    <section aria-label="Changes">
      <SectionHead title="Changes" count={events.length} why={`since ${cutoff}`} />
      {events.length === 0 ? (
        <p className="px-2 py-1 text-sm text-ink-3">Nothing changed here since the cutoff.</p>
      ) : (
        <ul className="text-sm">
          {events.map((e) => (
            <EventRow key={e.id} event={e} timeText={timeText(e.ts)} />
          ))}
        </ul>
      )}
    </section>
  )
}
