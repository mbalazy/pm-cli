import { Cloud } from 'lucide-react'
import { useState } from 'react'

import { useAttention, useConfig, useRemoteRuns, useRuns, useSolo } from '../api/queries'
import { RunsTable } from '../components/RunsTable'
import { SectionHead } from '../components/SectionHead'
import { SoloTable } from '../components/SoloTable'
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
import { soloRows } from '../lib/soloView'

// The Runs screen (and the group page's Runs tab, via `projects`): the /solo
// shifts first (pm-cli-136), then the executor's runs - only while
// cockpit.show_executor is on (pm-cli-137; the executor is frozen). Executor
// rows: local on every `runs` event, remote only on the button; ordered
// "needs me first" by default through the attention queue's own ranking,
// unfinished-only by default. Composition only - the rules are lib/runsView
// and lib/soloView.

interface Props {
  /** Restrict to these projects (a group's members); undefined = every project. */
  projects?: string[]
  /** The heading; the group tab passes none. */
  heading?: string
}

export function RunsPage({ projects, heading = 'Runs' }: Props) {
  const config = useConfig()
  const solo = useSolo()
  const showExecutor = config.data?.cockpit.show_executor === true
  const shifts = soloRows(solo.data?.shifts, projects)

  return (
    <div className="space-y-6">
      {heading && (
        <header className="flex flex-wrap items-baseline gap-x-4 border-b-2 border-ink pb-3">
          <h1 className="masthead">{heading}</h1>
          <span className="num text-ink-2">{shifts.length} solo shifts</span>
        </header>
      )}
      <section aria-label="Solo">
        <SectionHead title="Solo" count={shifts.length} why="/solo shifts · open first, newest" />
        {solo.isPending && <p className="text-ink-3">loading…</p>}
        {solo.isError && <p className="text-crit">error: {solo.error.message}</p>}
        {solo.data && <SoloTable rows={shifts} />}
      </section>
      {showExecutor ? (
        <ExecutorRuns projects={projects} />
      ) : (
        config.data && (
          <p className="text-xs text-ink-3 italic">
            executor runs hidden - switch them on in Settings (show executor runs)
          </p>
        )
      )}
    </div>
  )
}

function ExecutorRuns({ projects }: { projects?: string[] }) {
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
    <section aria-label="Executor runs" className="space-y-3">
      <SectionHead
        title="Executor"
        count={rows.length !== total ? `${rows.length} of ${total}` : rows.length}
        why="pm run-epic / pm work / pm finish"
      />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
        <span role="group" aria-label="sort" className="flex items-center gap-1">
          <span className="kicker mr-1">sort</span>
          {RUN_SORTS.map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={sort === s}
              className="pill"
              onClick={() => setSort(s)}
            >
              {sortLabel[s]}
            </button>
          ))}
        </span>
        <button
          type="button"
          aria-pressed={unfinishedOnly}
          className="pill"
          onClick={() => setUnfinishedOnly((v) => !v)}
          title="unfinished = a live run, or a row the attention queue puts in front of you"
        >
          {unfinishedOnly ? 'unfinished only' : 'all runs'}
        </button>
        <span className="flex items-center gap-2">
          <button
            type="button"
            className="ghost-btn"
            disabled={remote.isFetching}
            onClick={() => void remote.refetch()}
          >
            <Cloud aria-hidden="true" className="size-3" strokeWidth={1.75} />
            fetch remote
          </button>
          <span className="text-xs text-ink-3">remote: {remoteState}</span>
        </span>
      </div>
      {runs.isPending && <p className="text-ink-3">loading…</p>}
      {runs.isError && <p className="text-crit">error: {runs.error.message}</p>}
      {runs.data && <RunsTable rows={table} />}
    </section>
  )
}
