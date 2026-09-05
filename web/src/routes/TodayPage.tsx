import { useNavigate } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { useAttention, useChanges, useConfig, useFocus, useRefreshChanges } from '../api/queries'
import type { AttentionRow } from '../api/types'
import { AttentionSection } from '../components/AttentionSection'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { GroupChips } from '../components/GroupChips'
import { Toast } from '../components/Toast'
import { useConfirmedAction } from '../hooks/useConfirmedAction'
import { useShortcuts } from '../hooks/useShortcuts'
import { capSection, rowKey } from '../lib/attentionView'
import { isPendingKind } from '../lib/confirmText'
import { groupHotkeys, groupSearch } from '../lib/groupFilter'
import { refreshTimes } from '../lib/refreshTimes'
import { openTarget } from '../lib/rowActions'

// The home screen - the front page: a masthead with the date, then the
// attention queue in the API's section order, narrowed to one group when
// the URL says so. Composition only: hooks in, components out; the cap, the
// ages, the glyphs and the hotkeys are lib rules.

export function TodayPage({ group }: { group: string }) {
  const attention = useAttention('', group)
  // The unscoped queue (the sidebar's query, shared) lists every group for the chips.
  const all = useAttention()
  const changes = useChanges()
  const config = useConfig()
  const refresh = useRefreshChanges()
  const focus = useFocus()
  const action = useConfirmedAction()
  const navigate = useNavigate()
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [selectedKey, setSelectedKey] = useState<string>()

  const sections = attention.data?.sections ?? []
  const capped = sections.map((s) => ({ section: s, capped: capSection(s, expanded.has(s.name)) }))
  const flat = capped.flatMap((c) => c.capped.rows)
  const hotkeys = groupHotkeys(all.data?.groups ?? [])
  const times = refreshTimes(
    changes.data?.sources,
    config.data?.cockpit.refresh.every_seconds,
    config.data?.cockpit.refresh.window,
  )

  const step = (delta: number) => {
    if (flat.length === 0) return
    const i = flat.findIndex((r) => rowKey(r) === selectedKey)
    const next =
      i === -1 ? (delta > 0 ? 0 : flat.length - 1) : (i + delta + flat.length) % flat.length
    setSelectedKey(rowKey(flat[next]))
  }
  const selected = flat.find((r) => rowKey(r) === selectedKey)
  useShortcuts({
    down: () => step(1),
    up: () => step(-1),
    open: () => {
      // Only a row the API lets us open (a project-less Slack row has no page).
      if (selected?.actions.includes('open')) void navigate(openTarget(selected))
    },
    focus: () => {
      if (selected?.actions.includes('focus_toggle')) onAction('focus_toggle', selected)
    },
    group: (letter) => {
      const slug = hotkeys.get(letter)
      if (slug !== undefined) void navigate({ to: '/', search: groupSearch(slug) })
    },
  })

  const toggle = (name: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  // Every action the API lists and this build knows opens the dialog; the
  // run-control actions (pm-cli-118-21) stay disabled in the row itself.
  const onAction = (kind: string, row: AttentionRow) => {
    if (!isPendingKind(kind)) return
    action.ask({ kind, subject: row }, focus.data?.task_ids.includes(row.task_id ?? ''))
  }

  const today = new Date()
  const dateText = today.toLocaleDateString(undefined, {
    weekday: 'long',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })

  return (
    <div className="space-y-7">
      <header className="space-y-3 border-b-2 border-ink pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">Today</h1>
          <span className="display text-lg text-ink-2">{dateText}</span>
          {attention.data && (
            <span className="num ml-auto text-ink-2" title="doing tasks touched this week">
              WIP {attention.data.wip}
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-2">
          {times.refreshed && (
            <span className="num">
              refreshed {times.refreshed}
              {times.next && ` · next ${times.next}`}
            </span>
          )}
          {changes.isSuccess && (
            <button
              type="button"
              className="ghost-btn"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              <RefreshCw
                aria-hidden="true"
                className={refresh.isPending ? 'size-3 animate-spin' : 'size-3'}
                strokeWidth={1.75}
              />
              {refresh.isPending ? 'refreshing…' : 'refresh now'}
            </button>
          )}
          {refresh.isError && (
            <span className="text-crit">refresh failed: {refresh.error.message}</span>
          )}
        </div>
        {all.data && (
          <div className="hidden md:block">
            <GroupChips groups={all.data.groups} active={group} hotkeys={hotkeys} />
          </div>
        )}
      </header>
      {attention.isPending && <p className="text-ink-3">loading…</p>}
      {attention.isError && <p className="text-crit">error: {attention.error.message}</p>}
      {capped.map(({ section, capped: c }, i) => (
        <AttentionSection
          key={section.name}
          index={i}
          section={section}
          capped={c}
          expanded={expanded.has(section.name)}
          onToggle={() => toggle(section.name)}
          selectedKey={selectedKey}
          onAction={onAction}
        />
      ))}
      {attention.data && sections.length === 0 && (
        <p className="text-ink-3">every section is switched off in config.yaml</p>
      )}
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
