import { useNavigate } from '@tanstack/react-router'
import { CheckCheck, RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { useRowMutation } from '../api/mutations'
import { useAttention, useChanges, useConfig, useRefreshChanges, useReport } from '../api/queries'
import type { ChangeEvent, ReportSuggestion } from '../api/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EventRow } from '../components/EventRow'
import { GroupChips } from '../components/GroupChips'
import { ReportPanel } from '../components/ReportPanel'
import { SectionHead } from '../components/SectionHead'
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
import { isPendingKind } from '../lib/confirmText'
import { groupHotkeys, groupSearch } from '../lib/groupFilter'
import { reportView } from '../lib/reportView'
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
  const report = useReport()
  const dismiss = useRowMutation()
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
    <div className="space-y-7">
      <header className="space-y-3 border-b-2 border-ink pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">Changes</h1>
          {changes.data && (
            <span className="display text-lg text-ink-2">
              {cutoffText(changes.data.cutoff, now)}
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs text-ink-2">
          {cfg && (
            <span className="num">
              refresh every {Math.round(cfg.refresh.every_seconds / 60)} min, {cfg.refresh.window}
              {times.refreshed && ` · refreshed ${times.refreshed}`}
              {times.next && ` · next ${times.next}`}
            </span>
          )}
          <span className="ml-auto flex gap-1">
            <button
              type="button"
              className="ghost-btn"
              disabled={refresh.isPending || !changes.isSuccess}
              onClick={() => refresh.mutate()}
            >
              <RefreshCw
                aria-hidden="true"
                className={refresh.isPending ? 'size-3 animate-spin' : 'size-3'}
                strokeWidth={1.75}
              />
              {refresh.isPending ? 'refreshing…' : 'refresh now'}
            </button>
            <button
              type="button"
              className="ghost-btn"
              disabled={!changes.data || changes.data.unseen === 0}
              onClick={() => markSeen()}
            >
              <CheckCheck aria-hidden="true" className="size-3" strokeWidth={1.75} />
              mark all seen
              {changes.data && changes.data.unseen > 0 ? ` (${changes.data.unseen})` : ''}
            </button>
          </span>
        </div>
        {refresh.isError && (
          <p className="text-sm text-crit">refresh failed: {refresh.error.message}</p>
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

      <ReportPanel
        view={reportView(report.data)}
        onWrite={() =>
          action.ask({ kind: 'write_report', subject: { project: '', title: 'Report' } })
        }
        onDo={(s: ReportSuggestion) => {
          // The suggestion names one of the cockpit's own actions; "do" is
          // that action's ordinary dialog on the task it names.
          if (s.action && isPendingKind(s.action) && s.project) {
            action.ask({
              kind: s.action,
              subject: { project: s.project, task_id: s.task_id, title: s.text },
            })
          }
        }}
        onDismiss={(s: ReportSuggestion) => dismiss.mutate({ kind: 'report_dismiss', id: s.id })}
      />

      <section aria-label="Feed">
        <SectionHead
          title="Raw feed"
          count={`${shown.length}${shown.length !== all.length ? ` of ${all.length}` : ''}`}
          why="deterministic, zero tokens · unseen rows are highlighted"
        />
        {changes.isPending && <p className="text-ink-3">loading…</p>}
        {changes.isError && <p className="text-crit">error: {changes.error.message}</p>}
        {changes.data && shown.length === 0 && (
          <p className="px-2 py-1 text-sm text-ink-3">
            {all.length === 0
              ? 'Nothing changed since the cutoff (or nothing fetched yet - refresh now).'
              : 'Nothing matches the filters.'}
          </p>
        )}
        {changes.data && shown.length > 0 && (
          <ul className="text-sm">
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
        flags={action.flags}
        onFlagsChange={action.setFlags}
        busy={action.busy}
        error={action.error}
      />
      <Toast message={action.toast.message} error={action.toast.error} />
    </div>
  )
}
