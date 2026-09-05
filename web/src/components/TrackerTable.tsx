import { Link } from '@tanstack/react-router'

import { progressText } from '../lib/doingView'
import type { TrackerRow } from '../lib/groupView'
import { acceptCellText, runCellText } from '../lib/runCells'

// The group's trackers: the generated rollup (BuildTrackers, never a hand-kept
// table) plus the run / acceptance cells worded exactly as `pm runs` words
// them. Rows arrive joined (lib/groupView.trackerRows).

export function TrackerTable({ rows, showProject }: { rows: TrackerRow[]; showProject: boolean }) {
  return (
    <section aria-label="Trackers">
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
        <span className="font-semibold">Trackers</span>
        <span className="text-sm text-gray-500">{rows.length}</span>
        <span className="text-xs text-gray-400">
          generated rollup (BuildTrackers), as in pm_context
        </span>
      </h2>
      {rows.length === 0 ? (
        <p className="text-sm text-gray-500">No trackers.</p>
      ) : (
        <div className="overflow-x-auto">
          <table aria-label="Trackers" className="w-full text-left text-sm">
            <thead>
              <tr className="border-b text-gray-500">
                {showProject && <th className="py-1 pr-3">REPO</th>}
                <th className="py-1 pr-3">TRACKER</th>
                <th className="py-1 pr-3">TITLE</th>
                <th className="py-1 pr-3">PROGRESS</th>
                <th className="py-1 pr-3">RUN</th>
                <th className="py-1">ACCEPTANCE</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ project, tracker, run }) => (
                <tr key={`${project}/${tracker.id}`} className="border-b">
                  {showProject && <td className="py-1 pr-3">{project}</td>}
                  <td className="py-1 pr-3">
                    <Link
                      to="/p/$slug/t/$id"
                      params={{ slug: project, id: tracker.id }}
                      className="hover:underline"
                    >
                      <code>{tracker.id}</code>
                    </Link>
                  </td>
                  <td className="py-1 pr-3">{tracker.title}</td>
                  <td className="py-1 pr-3 whitespace-nowrap">
                    {progressText(tracker)} <span className="text-gray-400">/ {tracker.total}</span>
                  </td>
                  <td className="py-1 pr-3">{run ? runCellText(run.run) : '-'}</td>
                  <td className="py-1">{run ? acceptCellText(run.acceptance) : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
