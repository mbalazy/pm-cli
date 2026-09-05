import { Link } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'

import type { AttentionRow as Row } from '../api/types'
import { ageLabel } from '../lib/ageLabel'
import { rowGlyph } from '../lib/glyphs'
import { actionMeta, openTarget } from '../lib/rowActions'

// One attention row: glyph, project chip, id + title, the API's reason, the
// age, and a button per action the API listed. Nothing here decides what a
// row may do - `actions` is the API's, `enabled` is lib/rowActions'.

interface Props {
  row: Row
  selected: boolean
  /** Fires for an action this build can perform (open is handled as a link). */
  onAction?: (action: string, row: Row) => void
}

export function AttentionRow({ row, selected, onAction }: Props) {
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView?.({ block: 'nearest' })
  }, [selected])
  const target = openTarget(row)
  return (
    <li
      ref={ref}
      aria-current={selected ? 'true' : undefined}
      data-severity={row.severity}
      className={`flex flex-wrap items-baseline gap-x-2 gap-y-0.5 rounded px-1 py-0.5 ${selected ? 'bg-yellow-100' : ''}`}
    >
      <span className="inline-block w-4 text-center" aria-label={`severity: ${row.severity}`}>
        {rowGlyph(row)}
      </span>
      <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{row.project}</span>
      <Link to={target.to} params={target.params} className="hover:underline">
        {row.task_id && <code className="mr-1 text-sm text-gray-500">{row.task_id}</code>}
        {row.title}
      </Link>
      <span className="text-sm text-gray-600">{row.reason}</span>
      {row.flags?.map((f) => (
        <span key={f} className="rounded border px-1 text-xs uppercase text-gray-600">
          {f.replace('_', ' ')}
        </span>
      ))}
      <span className="ml-auto text-sm tabular-nums text-gray-500" title={row.since}>
        {ageLabel(row.age_seconds)}
      </span>
      <span className="flex gap-1">
        {row.actions.map((a) => {
          const meta = actionMeta(a)
          if (a === 'open') {
            return (
              <Link
                key={a}
                to={target.to}
                params={target.params}
                className="rounded border px-1 text-xs hover:underline"
              >
                {meta.label}
              </Link>
            )
          }
          return (
            <button
              key={a}
              type="button"
              className="rounded border px-1 text-xs disabled:text-gray-300"
              disabled={meta.pending !== ''}
              title={meta.pending || undefined}
              onClick={() => onAction?.(a, row)}
            >
              {meta.label}
            </button>
          )
        })}
      </span>
    </li>
  )
}
