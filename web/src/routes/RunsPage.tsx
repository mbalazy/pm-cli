import { useState } from 'react'

import { useAttention, useRemoteRuns, useRuns } from '../api/queries'
import { RunsTable } from '../components/RunsTable'
import { mergeRunRows } from '../lib/mergeRunRows'
import { relativeTime } from '../lib/relativeTime'
import {
  RUN_SORTS,
  filterProjects,
  filterRuns,
  needsMeIndex,
  runGlyph,
  runTimes,
  sortRuns,
  type RunSort,
} from '../lib/runsView'

// The Runs screen (and the group page's Runs tab, via `projects`): local
// rows on every `runs` event, remote rows only on the button; ordered
// "needs me first" by default through the attention queue's own ranking,
// unfinished-only by default. Composition only - the rules are lib/runsView.

interface Props {
  /** Restrict to these projects (a group's members); undefined = every project. */
  projects?: string[]
  /** The heading; the group tab passes none. */
  heading?: string
}

export function RunsPage({ projects, heading = 'Runs' }: Props) {
  const runs = useRuns()
  const remote = useRemoteRuns()
  const attention = useAttention()
  const [sort, setSort] = useState<RunSort>('needs_me')
  const [unfinishedOnly, setUnfinishedOnly] = useState(true)

  let remoteState = 'not fetched'
  if (remote.isFetching) remoteState = 'fetching…'
  else if (remote.isError) remoteState = `error: ${remote.error.message}`
  else if (remote.data)
    remoteState = `fetched ${relativeTime(new Date(remote.dataUpdatedAt).toISOString())}`

  const needsMe = needsMeIndex(attention.data?.sections.find((s) => s.name === 'needs_me')?.rows)
  const now = new Date()
  let rows = mergeRunRows(runs.data?.rows ?? [], remote.data?.rows)
  if (projects) rows = filterProjects(rows, projects)
  const total = rows.length
  rows = sortRuns(filterRuns(rows, unfinishedOnly, needsMe), sort, needsMe)
  const table = rows.map((row) => ({
    row,
    glyph: runGlyph(row, needsMe),
    times: runTimes(row, now),
  }))

  const sortLabel: Record<RunSort, string> = {
    needs_me: 'needs me first',
    newest: 'newest',
    project: 'by project',
  }

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-baseline gap-3">
        {heading && <h1 className="text-xl font-bold">{heading}</h1>}
        <span className="text-sm text-gray-500">
          {rows.length}
          {rows.length !== total && ` of ${total}`}
        </span>
        <span role="group" aria-label="sort" className="flex gap-1 text-sm">
          {RUN_SORTS.map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={sort === s}
              className={`rounded border px-2 ${sort === s ? 'font-bold' : ''}`}
              onClick={() => setSort(s)}
            >
              {sortLabel[s]}
            </button>
          ))}
        </span>
        <button
          type="button"
          aria-pressed={unfinishedOnly}
          className={`rounded border px-2 text-sm ${unfinishedOnly ? 'font-bold' : ''}`}
          onClick={() => setUnfinishedOnly((v) => !v)}
          title="unfinished = a live run, or a row the attention queue puts in front of you"
        >
          {unfinishedOnly ? 'unfinished only' : 'all runs'}
        </button>
        <button
          type="button"
          className="rounded border px-2 text-sm"
          disabled={remote.isFetching}
          onClick={() => void remote.refetch()}
        >
          fetch remote
        </button>
        <span className="text-sm text-gray-500">remote: {remoteState}</span>
      </div>
      {runs.isPending && <p>loading…</p>}
      {runs.isError && <p className="text-red-700">error: {runs.error.message}</p>}
      {runs.data && <RunsTable rows={table} />}
    </div>
  )
}
