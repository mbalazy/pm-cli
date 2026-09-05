import type { ChangeEvent } from '../api/types'
import { severityGlyph } from '../lib/glyphs'

// The change feed as a plain list (the full Changes screen is pm-cli-118-18;
// this is the group tab's view). Events arrive filtered and ordered.

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
            <li
              key={e.id}
              className={`flex flex-wrap items-baseline gap-x-2 px-1 ${e.seen ? 'text-gray-500' : ''}`}
            >
              <span className="inline-block w-4 text-center">{severityGlyph(e.severity)}</span>
              <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{e.source}</span>
              <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{e.project}</span>
              {e.task_id && <code className="text-gray-500">{e.task_id}</code>}
              <span>{e.title}</span>
              {e.detail && <span className="text-gray-500">{e.detail}</span>}
              {e.url && (
                <a href={e.url} target="_blank" rel="noreferrer" className="text-xs underline">
                  open
                </a>
              )}
              <span className="ml-auto tabular-nums text-gray-500">{timeText(e.ts)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
