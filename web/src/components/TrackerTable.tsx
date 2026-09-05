import { Link } from '@tanstack/react-router'

import { progressText } from '../lib/doingView'
import type { TrackerRow } from '../lib/groupView'
import { acceptCellText, runCellText } from '../lib/runCells'
import { SectionHead } from './SectionHead'

// The group's trackers: the generated rollup (BuildTrackers, never a hand-kept
// table) plus the run / acceptance cells worded exactly as `pm runs` words
// them. Rows arrive joined (lib/groupView.trackerRows).

export function TrackerTable({ rows, showProject }: { rows: TrackerRow[]; showProject: boolean }) {
  return (
    <section aria-label="Trackers">
      <SectionHead
        title="Trackers"
        count={rows.length}
        why="generated rollup (BuildTrackers), as in pm_context"
      />
      {rows.length === 0 ? (
        <p className="px-2 py-1 text-sm text-ink-3">No trackers.</p>
      ) : (
        <div className="overflow-x-auto">
          <table aria-label="Trackers" className="ledger-table">
            <thead>
              <tr>
                {showProject && <th>Repo</th>}
                <th>Tracker</th>
                <th>Title</th>
                <th>Progress</th>
                <th>Run</th>
                <th>Acceptance</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ project, tracker, run }) => (
                <tr key={`${project}/${tracker.id}`}>
                  {showProject && (
                    <td className="whitespace-nowrap">
                      <span className="chip">{project}</span>
                    </td>
                  )}
                  <td className="whitespace-nowrap">
                    <Link
                      to="/p/$slug/t/$id"
                      params={{ slug: project, id: tracker.id }}
                      className="hover:underline"
                    >
                      <code className="id text-ink">{tracker.id}</code>
                    </Link>
                  </td>
                  <td className="min-w-[16rem]">{tracker.title}</td>
                  <td className="num whitespace-nowrap">
                    {progressText(tracker)} <span className="text-ink-3">/ {tracker.total}</span>
                  </td>
                  <td className="whitespace-nowrap">{run ? runCellText(run.run) : '-'}</td>
                  <td className="whitespace-nowrap">
                    {run ? acceptCellText(run.acceptance) : '-'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
