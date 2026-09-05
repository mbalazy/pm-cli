import { Link } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'

import { cn } from '@/components/ui/cn'

import type { AttentionRow as Row } from '../api/types'
import { ageLabel } from '../lib/ageLabel'
import { rowGlyph } from '../lib/glyphs'
import { actionMeta, openTarget } from '../lib/rowActions'
import { ActionIcon } from './ActionIcon'
import { Glyph } from './Glyph'

// One attention row - a dispatch line of the ledger: glyph, project, id +
// title with the API's reason under it, the age, and a button per action the
// API listed. Nothing here decides what a row may do - `actions` is the
// API's, `enabled` is lib/rowActions'.

interface Props {
  row: Row
  selected: boolean
  /** Fires for an action this build can perform (open is handled as a link). */
  onAction?: (action: string, row: Row) => void
}

const FLAG_TONE: Record<string, string> = { no_reason: 'chip-crit', stale_plan: 'chip-warn' }

export function AttentionRow({ row, selected, onAction }: Props) {
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView?.({ block: 'nearest' })
  }, [selected])
  // `open` is the API's to offer (a project-less Slack row has no page to
  // open); a row without it renders its title as text, never as a link to
  // a route that does not exist.
  const canOpen = row.actions.includes('open')
  const target = openTarget(row)
  return (
    <li
      ref={ref}
      aria-current={selected ? 'true' : undefined}
      data-severity={row.severity}
      className="ledger-row grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-baseline gap-x-2 px-2 py-1.5 md:grid-cols-[1.25rem_7rem_minmax(0,1fr)_4rem]"
    >
      <Glyph glyph={rowGlyph(row)} severity={row.severity} label={`severity: ${row.severity}`} />
      <span className="chip hidden max-w-full truncate md:inline-flex" title={row.project}>
        {row.project}
      </span>
      <span className="min-w-0">
        {canOpen ? (
          <Link to={target.to} params={target.params} className="hover:underline">
            {row.task_id && <span className="id mr-1.5">{row.task_id}</span>}
            <span className="font-medium">{row.title}</span>
          </Link>
        ) : (
          <span>
            {row.task_id && <span className="id mr-1.5">{row.task_id}</span>}
            <span className="font-medium">{row.title}</span>
          </span>
        )}
        <span className="mt-0.5 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-[0.8125rem] text-ink-2">
          <span className="chip md:hidden">{row.project}</span>
          <span>{row.reason}</span>
          {row.flags?.map((f) => (
            <span key={f} className={cn('chip uppercase', FLAG_TONE[f])}>
              {f.replace('_', ' ')}
            </span>
          ))}
          <span className="row-actions flex flex-wrap gap-1 md:ml-auto">
            {row.actions.map((a) => {
              const meta = actionMeta(a)
              if (a === 'open') {
                return (
                  <Link key={a} to={target.to} params={target.params} className="ghost-btn">
                    <ActionIcon action={a} />
                    {meta.label}
                  </Link>
                )
              }
              return (
                <button
                  key={a}
                  type="button"
                  className="ghost-btn"
                  disabled={meta.pending !== ''}
                  title={meta.pending || undefined}
                  onClick={() => onAction?.(a, row)}
                >
                  <ActionIcon action={a} />
                  {meta.label}
                </button>
              )
            })}
          </span>
        </span>
      </span>
      <span className="num text-right whitespace-nowrap text-ink-2" title={row.since}>
        {ageLabel(row.age_seconds)}
      </span>
    </li>
  )
}
