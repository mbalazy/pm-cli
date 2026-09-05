import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { useAttention, useChanges, useConfig, useRefreshChanges } from '../api/queries'
import type { ChangeEvent } from '../api/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EventRow } from '../components/EventRow'
import { GroupChips } from '../components/GroupChips'
import { ReportPanel } from '../components/ReportPanel'
import { SourceChips } from '../components/SourceChips'
import { Toast } from '../components/Toast'
import { useConfirmedAction } from '../hooks/useConfirmedAction'
import { useShortcuts } from '../hooks/useShortcuts'
import {
  cutoffText,
  eventTimeText,
  filterEvents,
  sourceChips,
  sourceCounts,
  toggleSource,
} from '../lib/changesView'
import { groupHotkeys, groupSearch } from '../lib/groupFilter'
import { refreshTimes } from '../lib/refreshTimes'

// The Changes screen: the whole feed since the cutoff (off the server's
// cache), a source filter that doubles as the source status strip, a group
// filter in the URL (`?g=`, like home), "refresh now" and "mark seen"
// (through the dialog, like every mutation), and the report's place above
// the feed. Composition only: the filtering, the chips and the times are
// lib/changesView's.

export function ChangesPage({ group }: { group: string }) {
  const changes = useChanges()
  const config = useConfig()
  const attention = useAttention()
  const refresh = useRefreshChanges()
  const action = useConfirmedAction()
  const navigate = useNavigate()
  const [sources, setSources] = useState<Set<string>>(new Set())

  const hotkeys = groupHotkeys(attention.data?.groups ?? [])
  useShortcuts({
    group: (letter) => {
      const slug = hotkeys.get(letter)
      if (slug !== undefined) void navigate({ to: '/changes', search: groupSearch(slug) })
    },
  })

  const now = new Date()
  const all = changes.data?.events ?? []
  const byGroup = filterEvents(all, { sources: new Set(), group })
  const shown = filterEvents(byGroup, { sources, group: '' })
  const chips = sourceChips(changes.data?.sources, now)
  const names = chips.map((c) => c.name)
  const times = refreshTimes(changes.data?.sources, config.data?.cockpit.refresh.every_seconds)
  const cfg = config.data?.cockpit

  const markSeen = (e?: ChangeEvent) =>
    action.ask({
      kind: 'mark_seen',
      subject: e
        ? { project: e.project, task_id: e.task_id, title: e.title, since: e.ts }
        : { project: '', title: 'every change since the cutoff' },
    })

  return (
    <div className="space-y-4">
      <header className="space-y-2">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-xl font-bold">Changes</h1>
          {changes.data && (
            <span className="text-sm text-gray-600">{cutoffText(changes.data.cutoff, now)}</span>
          )}
          {cfg && (
            <span className="text-xs text-gray-400">
              refresh every {Math.round(cfg.refresh.every_seconds / 60)} min, {cfg.refresh.window}
              {times.refreshed && ` · refreshed ${times.refreshed}`}
              {times.next && ` · next ${times.next}`}
            </span>
          )}
          <span className="ml-auto flex gap-2">
            <button
              type="button"
              className="rounded border px-2 text-sm"
              disabled={refresh.isPending || !changes.isSuccess}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? 'refreshing…' : 'refresh now'}
            </button>
            <button
              type="button"
              className="rounded border px-2 text-sm"
              disabled={!changes.data || changes.data.unseen === 0}
              onClick={() => markSeen()}
            >
              mark all seen
              {changes.data && changes.data.unseen > 0 ? ` (${changes.data.unseen})` : ''}
            </button>
          </span>
        </div>
        {refresh.isError && (
          <p className="text-sm text-red-700">refresh failed: {refresh.error.message}</p>
        )}
        {changes.data && (
          <SourceChips
            chips={chips}
            counts={sourceCounts(byGroup)}
            selected={sources}
            onToggle={(s) => setSources(toggleSource(sources, s, names))}
          />
        )}
        {attention.data && (
          <GroupChips
            to="/changes"
            groups={attention.data.groups}
            active={group}
            hotkeys={hotkeys}
          />
        )}
      </header>

      <ReportPanel enabled={cfg?.sources.report === true} />

      <section aria-label="Feed">
        <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
          <span className="font-semibold">Raw feed</span>
          <span className="text-sm text-gray-500">
            {shown.length}
            {shown.length !== all.length && ` of ${all.length}`}
          </span>
          <span className="text-xs text-gray-400">
            deterministic, zero tokens · unseen rows are highlighted
          </span>
        </h2>
        {changes.isPending && <p>loading…</p>}
        {changes.isError && <p className="text-red-700">error: {changes.error.message}</p>}
        {changes.data && shown.length === 0 && (
          <p className="text-sm text-gray-500">
            {all.length === 0
              ? 'Nothing changed since the cutoff (or nothing fetched yet - refresh now).'
              : 'Nothing matches the filters.'}
          </p>
        )}
        {changes.data && shown.length > 0 && (
          <ul className="space-y-0.5 text-sm">
            {shown.map((e) => (
              <EventRow
                key={e.id}
                event={e}
                timeText={eventTimeText(e.ts, now)}
                onSeen={markSeen}
              />
            ))}
          </ul>
        )}
      </section>

      <ConfirmDialog
        open={action.pending !== null}
        text={action.text}
        value={action.value}
        onChange={action.setValue}
        onConfirm={action.confirm}
        onCancel={action.cancel}
        busy={action.busy}
        error={action.error}
      />
      <Toast message={action.toast.message} error={action.toast.error} />
    </div>
  )
}
