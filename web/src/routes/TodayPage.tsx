import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { useAttention, useChanges, useConfig, useRefreshChanges } from '../api/queries'
import type { AttentionRow } from '../api/types'
import { AttentionSection } from '../components/AttentionSection'
import { GroupChips } from '../components/GroupChips'
import { useShortcuts } from '../hooks/useShortcuts'
import { capSection, rowKey } from '../lib/attentionView'
import { groupHotkeys, groupSearch } from '../lib/groupFilter'
import { refreshTimes } from '../lib/refreshTimes'
import { openTarget } from '../lib/rowActions'

// The home screen: the attention queue in the API's section order, narrowed
// to one group when the URL says so. Composition only: hooks in, components
// out; the cap, the ages, the glyphs and the hotkeys are lib rules.

export function TodayPage({ group }: { group: string }) {
  const attention = useAttention('', group)
  // The unscoped queue (the sidebar's query, shared) lists every group for the chips.
  const all = useAttention()
  const changes = useChanges()
  const config = useConfig()
  const refresh = useRefreshChanges()
  const navigate = useNavigate()
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [selectedKey, setSelectedKey] = useState<string>()

  const sections = attention.data?.sections ?? []
  const capped = sections.map((s) => ({ section: s, capped: capSection(s, expanded.has(s.name)) }))
  const flat = capped.flatMap((c) => c.capped.rows)
  const hotkeys = groupHotkeys(all.data?.groups ?? [])
  const times = refreshTimes(changes.data?.sources, config.data?.cockpit.refresh.every_seconds)

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
      if (selected) void navigate(openTarget(selected))
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
  // Mutations land in pm-cli-118-16/-21; this build only renders the buttons.
  const onAction = (_action: string, _row: AttentionRow) => {}

  const today = new Date()
  const dateText = today.toLocaleDateString(undefined, {
    weekday: 'long',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })

  return (
    <div className="space-y-6">
      <header className="space-y-2">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-xl font-bold">Today</h1>
          <span className="text-sm text-gray-600">{dateText}</span>
          {times.refreshed && (
            <span className="text-sm text-gray-500">
              refreshed {times.refreshed}
              {times.next && ` · next ${times.next}`}
            </span>
          )}
          {changes.isSuccess && (
            <button
              type="button"
              className="rounded border px-2 text-sm"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? 'refreshing…' : 'refresh now'}
            </button>
          )}
          {refresh.isError && (
            <span className="text-sm text-red-700">refresh failed: {refresh.error.message}</span>
          )}
          {attention.data && (
            <span className="ml-auto text-sm text-gray-600" title="doing tasks touched this week">
              WIP {attention.data.wip}
            </span>
          )}
        </div>
        {all.data && (
          <div className="hidden md:block">
            <GroupChips groups={all.data.groups} active={group} hotkeys={hotkeys} />
          </div>
        )}
      </header>
      {attention.isPending && <p>loading…</p>}
      {attention.isError && <p className="text-red-700">error: {attention.error.message}</p>}
      {capped.map(({ section, capped: c }) => (
        <AttentionSection
          key={section.name}
          section={section}
          capped={c}
          expanded={expanded.has(section.name)}
          onToggle={() => toggle(section.name)}
          selectedKey={selectedKey}
          onAction={onAction}
        />
      ))}
      {attention.data && sections.length === 0 && (
        <p className="text-gray-500">every section is switched off in config.yaml</p>
      )}
    </div>
  )
}
