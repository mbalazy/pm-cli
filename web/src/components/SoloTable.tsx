import { Link, useNavigate } from '@tanstack/react-router'
import { Copy } from 'lucide-react'

import type { SoloLaunch } from '../api/types'
import type { SoloRow } from '../lib/soloView'
import { toneClass } from './tone'
import { cn } from './ui/cn'

// The solo shifts table: glyph, project, date, what the shift was, how it
// came out, when it closed, the session (a launch from the cockpit: its
// supervisor state and the attach command to paste), the report link and a
// stop button. The whole row opens the report (a launch without a shift
// file has none yet); the project opens that repo's board. Rows come worded
// by lib/soloView.

interface Props {
  rows: SoloRow[]
  /** Asked when the stop button of a live launch is pressed (the page opens the dialog). */
  onStop?: (launch: SoloLaunch) => void
}

export function SoloTable({ rows, onStop }: Props) {
  const navigate = useNavigate()
  if (rows.length === 0) return <p className="px-2 py-1 text-sm text-ink-3">no solo shifts</p>
  const copy = (text: string) => {
    try {
      void navigator.clipboard?.writeText(text)
    } catch {
      /* no clipboard (an insecure context): the text is selectable anyway */
    }
  }
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
            <th>session</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const report = { project: r.shift.project, shift: r.shift.id }
            return (
              <tr
                key={`${r.shift.project}/${r.shift.id}`}
                className={cn(r.hasFile && 'cursor-pointer hover:bg-[var(--paper-2)]')}
                onClick={(e) => {
                  // A click on a link or a button inside the row is that control's.
                  if ((e.target as HTMLElement).closest('a, button')) return
                  if (r.hasFile) void navigate({ to: '/solo/$project/$shift', params: report })
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
                  {r.hasFile ? (
                    <Link
                      to="/solo/$project/$shift"
                      params={report}
                      className="underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                    >
                      {r.title || r.shift.id}
                    </Link>
                  ) : (
                    <span title={r.shift.status_line}>{r.title || r.shift.id}</span>
                  )}
                </td>
                <td className={cn('whitespace-nowrap text-sm', r.tone && toneClass(r.tone))}>
                  {r.outcome || '-'}
                </td>
                <td className="whitespace-nowrap text-ink-2">{r.when}</td>
                <td className="text-sm">
                  {r.launch ? (
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span
                        className={cn(
                          'whitespace-nowrap',
                          r.launch.state === 'blocked' && 'sev-warn',
                          (r.launch.state === 'error' || r.launch.state === 'failed') && 'sev-crit',
                        )}
                      >
                        {r.session}
                      </span>
                      {r.attach && (
                        <span className="flex items-center gap-1">
                          <code
                            className="rounded-sm bg-paper-2 px-1 font-mono text-xs"
                            title={r.launch.logs}
                          >
                            {r.attach}
                          </code>
                          <button
                            type="button"
                            className="ghost-btn"
                            aria-label={`copy ${r.attach}`}
                            title="copy the attach command"
                            onClick={() => copy(r.attach)}
                          >
                            <Copy aria-hidden="true" className="size-3" strokeWidth={1.75} />
                          </button>
                        </span>
                      )}
                    </span>
                  ) : (
                    <span className="text-ink-3">-</span>
                  )}
                </td>
                <td className="whitespace-nowrap">
                  {r.hasFile && (
                    <Link to="/solo/$project/$shift" params={report} className="ghost-btn">
                      {r.shift.report ? 'report' : 'state'}
                    </Link>
                  )}
                  {r.launch && r.live && onStop && (
                    <button type="button" className="ghost-btn" onClick={() => onStop(r.launch!)}>
                      stop
                    </button>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
