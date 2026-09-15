import { Link } from '@tanstack/react-router'

import type { SoloRow } from '../lib/soloView'
import { toneClass } from './tone'
import { cn } from './ui/cn'

// The solo shifts table: glyph, project, date, what the shift was, how it
// came out, when it closed, and the report link. Rows come worded by
// lib/soloView.

export function SoloTable({ rows }: { rows: SoloRow[] }) {
  if (rows.length === 0) return <p className="px-2 py-1 text-sm text-ink-3">no solo shifts</p>
  return (
    <div className="overflow-x-auto">
      <table aria-label="Solo shifts" className="ledger-table">
        <thead>
          <tr>
            <th className="w-6" aria-label="state" />
            <th>project</th>
            <th>date</th>
            <th>shift</th>
            <th>outcome</th>
            <th>state</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={`${r.shift.project}/${r.shift.id}`}>
              <td className="text-center">{r.glyph}</td>
              <td className="font-mono text-xs">{r.shift.project}</td>
              <td className="num whitespace-nowrap">{r.shift.date}</td>
              <td className="text-sm">{r.title || '-'}</td>
              <td className={cn('whitespace-nowrap text-sm', r.tone && toneClass(r.tone))}>
                {r.outcome || '-'}
              </td>
              <td className="whitespace-nowrap text-ink-2">{r.when}</td>
              <td>
                <Link
                  to="/solo/$project/$shift"
                  params={{ project: r.shift.project, shift: r.shift.id }}
                  className="ghost-btn"
                >
                  {r.shift.report ? 'report' : 'state'}
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
