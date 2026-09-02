import type { RunRow } from '../api/types'
import { acceptCellText, runCellText } from '../lib/runCells'

// The `pm runs` table: PROJECT / TRACKER / TITLE / RUN / ACCEPTANCE, cells
// worded by lib/runCells so this page and the CLI never disagree.

export function RunsTable({ rows }: { rows: RunRow[] }) {
  if (rows.length === 0) return <p className="text-gray-500">no runs</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b text-gray-500">
            <th className="py-1 pr-3">PROJECT</th>
            <th className="py-1 pr-3">TRACKER</th>
            <th className="py-1 pr-3">TITLE</th>
            <th className="py-1 pr-3">RUN</th>
            <th className="py-1">ACCEPTANCE</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={`${r.remote ?? ''}/${r.project}/${r.tracker ?? i}`} className="border-b">
              <td className="py-1 pr-3">{r.remote ? `${r.remote}/${r.project}` : r.project}</td>
              <td className="py-1 pr-3">
                <code>{r.tracker}</code>
              </td>
              <td className="py-1 pr-3">
                {r.note ? <i className="text-gray-500">{r.note}</i> : r.title}
              </td>
              <td className="py-1 pr-3">{runCellText(r.run)}</td>
              <td className="py-1">{acceptCellText(r.acceptance)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
