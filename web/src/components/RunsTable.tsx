import { Link } from '@tanstack/react-router'

import type { RunRow } from '../api/types'
import { acceptCellText, runCellText } from '../lib/runCells'
import type { RunTimes } from '../lib/runsView'
import { toneClass } from './tone'

// The `pm runs` table plus the cockpit's columns: a fixed-width glyph first,
// then PROJECT / TRACKER / TITLE / RUN / ACCEPTANCE and the three times
// (duration · since end · heartbeat). Cells are worded by lib/runCells and
// lib/runsView so this page, the CLI and the board never disagree. Rows come
// ordered and filtered; the glyph and the times come computed. A local row's
// project opens its board, its tracker and title open the task; a remote
// row lives on another machine and links nowhere.

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

const LINK = 'underline decoration-rule-strong underline-offset-2 hover:decoration-ink'

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
          {rows.map(({ row: r, glyph, times }, i) => {
            const task = r.tracker && !r.remote ? { slug: r.project, id: r.tracker } : undefined
            return (
              <tr
                key={`${r.remote ?? ''}/${r.project}/${r.tracker ?? i}`}
                className="hover:bg-[var(--paper-2)]"
              >
                <td className={`w-5 text-center font-mono ${glyphTone(glyph)}`}>{glyph}</td>
                <td className="whitespace-nowrap">
                  {r.remote ? (
                    <span className="chip">{`${r.remote}/${r.project}`}</span>
                  ) : (
                    <Link
                      to="/p/$slug"
                      params={{ slug: r.project }}
                      className="chip hover:underline"
                    >
                      {r.project}
                    </Link>
                  )}
                </td>
                <td className="whitespace-nowrap">
                  {task ? (
                    <Link to="/p/$slug/t/$id" params={task} className={LINK}>
                      <code className="id text-ink">{r.tracker}</code>
                    </Link>
                  ) : (
                    <code className="id text-ink">{r.tracker}</code>
                  )}
                </td>
                <td className="min-w-[16rem]">
                  {r.note ? (
                    <i className="text-ink-3">{r.note}</i>
                  ) : task ? (
                    <Link to="/p/$slug/t/$id" params={task} className={LINK}>
                      {r.title}
                    </Link>
                  ) : (
                    r.title
                  )}
                </td>
                <td className="whitespace-nowrap">{runCellText(r.run)}</td>
                <td className="whitespace-nowrap">{acceptCellText(r.acceptance)}</td>
                <td className="num text-ink-2">{times.duration}</td>
                <td className="num text-ink-2">{times.sinceEnd}</td>
                <td className="num text-ink-2">{times.heartbeat}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
