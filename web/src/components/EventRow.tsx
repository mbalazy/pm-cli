import { Link } from '@tanstack/react-router'
import { ExternalLink, Eye } from 'lucide-react'

import { cn } from '@/components/ui/cn'

import type { ChangeEvent } from '../api/types'
import { severityGlyph } from '../lib/glyphs'
import { Glyph } from './Glyph'

// One feed event - a wire line: severity glyph, source and project chips,
// id + title, detail, an external link when the event has a url, the time,
// and the optional "seen" button (the Changes screen passes it; the group
// tab does not). An unseen event is drawn stronger; a seen one dimmed.

interface Props {
  event: ChangeEvent
  timeText: string
  onSeen?: (e: ChangeEvent) => void
}

export function EventRow({ event: e, timeText, onSeen }: Props) {
  return (
    <li
      data-seen={e.seen}
      className={cn(
        'ledger-row grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-baseline gap-x-2 px-2 py-1',
        e.seen ? 'text-ink-2' : 'bg-info-bg/40',
      )}
    >
      <Glyph glyph={severityGlyph(e.severity)} severity={e.seen ? undefined : e.severity} />
      <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="chip">{e.source}</span>
        {e.project && <span className="chip">{e.project}</span>}
        {e.task_id ? (
          <Link
            to="/p/$slug/t/$id"
            params={{ slug: e.project, id: e.task_id }}
            className="hover:underline"
          >
            <span className="id mr-1.5">{e.task_id}</span>
            <span className={e.seen ? '' : 'font-medium'}>{e.title}</span>
          </Link>
        ) : (
          <span className={e.seen ? '' : 'font-medium'}>{e.title}</span>
        )}
        {e.detail && <span className="text-[0.8125rem] text-ink-2">{e.detail}</span>}
        {e.url && (
          <a
            href={e.url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-0.5 text-xs underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
          >
            open
            <ExternalLink aria-hidden="true" className="size-3 text-ink-3" />
          </a>
        )}
      </span>
      <span className="flex items-baseline gap-2">
        <span className="num whitespace-nowrap text-ink-3" title={e.ts}>
          {timeText}
        </span>
        {onSeen && !e.seen && (
          <button
            type="button"
            className="ghost-btn"
            title="mark everything up to this one as seen"
            onClick={() => onSeen(e)}
          >
            <Eye aria-hidden="true" className="size-3" strokeWidth={1.75} />
            seen
          </button>
        )}
      </span>
    </li>
  )
}
