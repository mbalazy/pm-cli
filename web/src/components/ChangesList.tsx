import type { ChangeEvent } from '../api/types'
import { EventRow } from './EventRow'

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
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
        <span className="font-semibold">Changes</span>
        <span className="text-sm text-gray-500">{events.length}</span>
        <span className="text-xs text-gray-400">since {cutoff}</span>
      </h2>
      {events.length === 0 ? (
        <p className="text-sm text-gray-500">Nothing changed here since the cutoff.</p>
      ) : (
        <ul className="space-y-0.5 text-sm">
          {events.map((e) => (
            <EventRow key={e.id} event={e} timeText={timeText(e.ts)} />
          ))}
        </ul>
      )}
    </section>
  )
}
