import { Link, useNavigate } from '@tanstack/react-router'

import type { SoloRow } from '../lib/soloView'
import { toneClass } from './tone'
import { cn } from './ui/cn'

// The solo shifts table: glyph, project, date, what the shift was, how it
// came out, when it closed, and the report link. The whole row opens the
// report; the project opens that repo's board. Rows come worded by
// lib/soloView.

export function SoloTable({ rows }: { rows: SoloRow[] }) {
  const navigate = useNavigate()
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
          {rows.map((r) => {
            const report = { project: r.shift.project, shift: r.shift.id }
            return (
              <tr
                key={`${r.shift.project}/${r.shift.id}`}
                className="cursor-pointer hover:bg-[var(--paper-2)]"
                onClick={(e) => {
                  // A click on a link inside the row is that link's.
                  if ((e.target as HTMLElement).closest('a')) return
                  void navigate({ to: '/solo/$project/$shift', params: report })
                }}
              >
                <td className="text-center">{r.glyph}</td>
                <td>
                  <Link
                    to="/p/$slug"
                    params={{ slug: r.shift.project }}
                    className="chip hover:underline"
                  >
                    {r.shift.project}
                  </Link>
                </td>
                <td className="num whitespace-nowrap">{r.shift.date}</td>
                <td className="text-sm">
                  <Link
                    to="/solo/$project/$shift"
                    params={report}
                    className="underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                  >
                    {r.title || r.shift.id}
                  </Link>
                </td>
                <td className={cn('whitespace-nowrap text-sm', r.tone && toneClass(r.tone))}>
                  {r.outcome || '-'}
                </td>
                <td className="whitespace-nowrap text-ink-2">{r.when}</td>
                <td>
                  <Link to="/solo/$project/$shift" params={report} className="ghost-btn">
                    {r.shift.report ? 'report' : 'state'}
                  </Link>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
