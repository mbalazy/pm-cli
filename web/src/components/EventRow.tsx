import { Link } from '@tanstack/react-router'

import type { ChangeEvent } from '../api/types'
import { severityGlyph } from '../lib/glyphs'

// One feed event: severity glyph, source and project chips, id + title,
// detail, an external link when the event has a url, the time, and the
// optional "seen" button (the Changes screen passes it; the group tab does
// not). An unseen event is drawn stronger; a seen one dimmed.

interface Props {
  event: ChangeEvent
  timeText: string
  onSeen?: (e: ChangeEvent) => void
}

export function EventRow({ event: e, timeText, onSeen }: Props) {
  return (
    <li
      data-seen={e.seen}
      className={`flex flex-wrap items-baseline gap-x-2 rounded px-1 ${e.seen ? 'text-gray-500' : 'bg-blue-50/40'}`}
    >
      <span className="inline-block w-4 text-center">{severityGlyph(e.severity)}</span>
      <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{e.source}</span>
      {e.project && (
        <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{e.project}</span>
      )}
      {e.task_id ? (
        <Link
          to="/p/$slug/t/$id"
          params={{ slug: e.project, id: e.task_id }}
          className="hover:underline"
        >
          <code className="mr-1 text-gray-500">{e.task_id}</code>
          {e.title}
        </Link>
      ) : (
        <span className={e.seen ? '' : 'font-medium'}>{e.title}</span>
      )}
      {e.detail && <span className="text-gray-500">{e.detail}</span>}
      {e.url && (
        <a href={e.url} target="_blank" rel="noreferrer" className="text-xs underline">
          open
        </a>
      )}
      <span className="ml-auto tabular-nums text-gray-500" title={e.ts}>
        {timeText}
      </span>
      {onSeen && !e.seen && (
        <button
          type="button"
          className="rounded border px-1 text-xs"
          title="mark everything up to this one as seen"
          onClick={() => onSeen(e)}
        >
          seen
        </button>
      )}
    </li>
  )
}
