import { Link } from '@tanstack/react-router'

import type { RunRow } from '../api/types'
import { acceptCellText, runCellText } from '../lib/runCells'
import type { RunTimes } from '../lib/runsView'

// The `pm runs` table plus the cockpit's columns: a fixed-width glyph first,
// then PROJECT / TRACKER / TITLE / RUN / ACCEPTANCE and the three times
// (duration · since end · heartbeat). Cells are worded by lib/runCells and
// lib/runsView so this page, the CLI and the board never disagree. Rows come
// ordered and filtered; the glyph and the times come computed.

export interface RunsTableRow {
  row: RunRow
  glyph: string
  times: RunTimes
}

export function RunsTable({ rows }: { rows: RunsTableRow[] }) {
  if (rows.length === 0) return <p className="text-gray-500">no runs</p>
  return (
    <div className="overflow-x-auto">
      <table aria-label="Runs" className="w-full text-left text-sm">
        <thead>
          <tr className="border-b text-gray-500">
            <th className="w-5 py-1" aria-label="state" />
            <th className="py-1 pr-3">PROJECT</th>
            <th className="py-1 pr-3">TRACKER</th>
            <th className="py-1 pr-3">TITLE</th>
            <th className="py-1 pr-3">RUN</th>
            <th className="py-1 pr-3">ACCEPTANCE</th>
            <th className="py-1 pr-3" title="how long the run ran">
              DURATION
            </th>
            <th className="py-1 pr-3" title="since the run ended">
              ENDED
            </th>
            <th className="py-1" title="since the manager's last heartbeat (live runs)">
              HEARTBEAT
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ row: r, glyph, times }, i) => (
            <tr key={`${r.remote ?? ''}/${r.project}/${r.tracker ?? i}`} className="border-b">
              <td className="w-5 py-1 text-center">{glyph}</td>
              <td className="py-1 pr-3">{r.remote ? `${r.remote}/${r.project}` : r.project}</td>
              <td className="py-1 pr-3">
                {r.tracker && !r.remote ? (
                  <Link
                    to="/p/$slug/t/$id"
                    params={{ slug: r.project, id: r.tracker }}
                    className="hover:underline"
                  >
                    <code>{r.tracker}</code>
                  </Link>
                ) : (
                  <code>{r.tracker}</code>
                )}
              </td>
              <td className="py-1 pr-3">
                {r.note ? <i className="text-gray-500">{r.note}</i> : r.title}
              </td>
              <td className="py-1 pr-3 whitespace-nowrap">{runCellText(r.run)}</td>
              <td className="py-1 pr-3">{acceptCellText(r.acceptance)}</td>
              <td className="py-1 pr-3 tabular-nums">{times.duration}</td>
              <td className="py-1 pr-3 tabular-nums">{times.sinceEnd}</td>
              <td className="py-1 tabular-nums">{times.heartbeat}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
