import { useRemoteRuns, useRuns } from '../api/queries'
import { RunsTable } from '../components/RunsTable'
import { mergeRunRows } from '../lib/mergeRunRows'
import { relativeTime } from '../lib/relativeTime'

export function RunsPage() {
  const runs = useRuns()
  const remote = useRemoteRuns()

  let remoteState = 'not fetched'
  if (remote.isFetching) remoteState = 'fetching…'
  else if (remote.isError) remoteState = `error: ${remote.error.message}`
  else if (remote.data)
    remoteState = `fetched ${relativeTime(new Date(remote.dataUpdatedAt).toISOString())}`

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-baseline gap-3">
        <h1 className="text-xl font-bold">Runs</h1>
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
      {runs.data && <RunsTable rows={mergeRunRows(runs.data.rows, remote.data?.rows)} />}
    </div>
  )
}
