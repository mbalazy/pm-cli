import { Link } from '@tanstack/react-router'

import type { RunRow } from '../api/types'
import { acceptCellText, runCellText } from '../lib/runCells'
import type { RunTimes } from '../lib/runsView'
import { toneClass } from './Glyph'

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

/** The glyph's ink: the severity glyphs of lib/glyphs, ▶ for a live run. */
function glyphTone(glyph: string): string {
  switch (glyph) {
    case '✗':
      return toneClass('crit')
    case '▲':
      return toneClass('warn')
    case '●':
    case '▶':
      return toneClass('info')
    default:
      return toneClass(undefined)
  }
}

export function RunsTable({ rows }: { rows: RunsTableRow[] }) {
  if (rows.length === 0) return <p className="px-2 py-1 text-sm text-ink-3">no runs</p>
  return (
    <div className="overflow-x-auto">
      <table aria-label="Runs" className="ledger-table">
        <thead>
          <tr>
            <th className="w-5" aria-label="state" />
            <th>Project</th>
            <th>Tracker</th>
            <th>Title</th>
            <th>Run</th>
            <th>Acceptance</th>
            <th title="how long the run ran">Duration</th>
            <th title="since the run ended">Ended</th>
            <th title="since the manager's last heartbeat (live runs)">Heartbeat</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ row: r, glyph, times }, i) => (
            <tr key={`${r.remote ?? ''}/${r.project}/${r.tracker ?? i}`}>
              <td className={`w-5 text-center font-mono ${glyphTone(glyph)}`}>{glyph}</td>
              <td className="whitespace-nowrap">
                <span className="chip">{r.remote ? `${r.remote}/${r.project}` : r.project}</span>
              </td>
              <td className="whitespace-nowrap">
                {r.tracker && !r.remote ? (
                  <Link
                    to="/p/$slug/t/$id"
                    params={{ slug: r.project, id: r.tracker }}
                    className="hover:underline"
                  >
                    <code className="id text-ink">{r.tracker}</code>
                  </Link>
                ) : (
                  <code className="id text-ink">{r.tracker}</code>
                )}
              </td>
              <td className="min-w-[16rem]">
                {r.note ? <i className="text-ink-3">{r.note}</i> : r.title}
              </td>
              <td className="whitespace-nowrap">{runCellText(r.run)}</td>
              <td className="whitespace-nowrap">{acceptCellText(r.acceptance)}</td>
              <td className="num text-ink-2">{times.duration}</td>
              <td className="num text-ink-2">{times.sinceEnd}</td>
              <td className="num text-ink-2">{times.heartbeat}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
