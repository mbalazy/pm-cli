import { useQueries } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'

import { cn } from '@/components/ui/cn'

import {
  contextQuery,
  tasksQuery,
  useAttention,
  useChanges,
  useConfig,
  useProjects,
  useRuns,
} from '../api/queries'
import type { AttentionRow, Project } from '../api/types'
import { AttentionSection } from '../components/AttentionSection'
import { ChangesList } from '../components/ChangesList'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { DoingList } from '../components/DoingList'
import { LeftOff } from '../components/LeftOff'
import { Toast } from '../components/Toast'
import { TrackerTable } from '../components/TrackerTable'
import { useConfirmedAction } from '../hooks/useConfirmedAction'
import { useShortcuts } from '../hooks/useShortcuts'
import { ageLabel, clockLabel } from '../lib/ageLabel'
import { useRowMutation } from '../api/mutations'
import { capSection, dismissRowOf } from '../lib/attentionView'
import { isPendingKind } from '../lib/confirmText'
import { activityAgeSeconds, doingByActivity, isIdle, trackerIndex } from '../lib/doingView'
import {
  GROUP_TABS,
  activeRepo,
  filterChanges,
  groupMembers,
  groupName,
  stepTab,
  trackerRows,
  type GroupTab,
} from '../lib/groupView'
import { relativeTime } from '../lib/relativeTime'
import { BoardPane } from './BoardPane'
import { RunsPage } from './RunsPage'

// The group page (a client/product = one or more repos): header, repo chips,
// four tabs in the URL (`?tab=`, `?repo=` for the board). Composition only:
// the members, the tab, the doing order, the joins are lib/groupView and
// lib/doingView; the attention rows are the API's, narrowed by `?group=`.

interface Props {
  group: string
  tab: GroupTab
  repo?: string
}

export function GroupPage({ group, tab, repo }: Props) {
  const projects = useProjects()
  const attention = useAttention('', group)
  const config = useConfig()
  const runs = useRuns()
  const changes = useChanges()
  const action = useConfirmedAction()
  const rows = useRowMutation()
  const navigate = useNavigate()

  const members = groupMembers(projects.data?.projects, group)
  const slugs = members.map((m) => m.slug)
  const taskLists = useQueries({ queries: slugs.map((s) => tasksQuery(s)) })
  const contexts = useQueries({ queries: slugs.map((s) => contextQuery(s)) })

  const go = (t: GroupTab) =>
    void navigate({ to: '/g/$group', params: { group }, search: { tab: t, repo } })
  useShortcuts({ tabPrev: () => go(stepTab(tab, -1)), tabNext: () => go(stepTab(tab, 1)) })

  if (projects.isPending) return <p className="text-ink-3">loading…</p>
  if (projects.isError) return <p className="text-crit">error: {projects.error.message}</p>
  if (members.length === 0)
    return <p className="text-crit">error: no active project in group {group}</p>

  const summary = attention.data?.groups.find((g) => g.slug === group)
  const lastActivity = summary?.last_activity ? relativeTime(summary.last_activity) : ''
  const name = groupName(members, group)
  const idleDays = config.data?.cockpit.doing_idle_days ?? 7
  const now = new Date()

  const onAction = (kind: string, row: AttentionRow) => {
    if (kind === 'dismiss') rows.mutate({ kind: 'dismiss', rows: [dismissRowOf(row)] })
    else if (isPendingKind(kind)) action.ask({ kind, subject: row })
  }
  const onDismissRows = (rs: AttentionRow[]) =>
    rows.mutate({ kind: 'dismiss', rows: rs.map(dismissRowOf) })
  const editNotes = (p: Project) =>
    action.ask({ kind: 'edit_notes', subject: { project: p.slug, title: p.name, notes: p.notes } })

  const section = (nameOf: string) => attention.data?.sections.find((s) => s.name === nameOf)
  const needsMe = section('needs_me')
  const waiting = section('waiting')

  const doing = doingByActivity(taskLists.map((q) => q.data?.tasks ?? []))
  const trackers = trackerIndex(contexts.map((q) => q.data?.trackers))
  const doingRows = doing.map((task) => ({
    task,
    idle: isIdle(task, idleDays, now),
    ageText: ageLabel(activityAgeSeconds(task, now)),
    tracker: trackers.get(task.id),
  }))
  const trackerList = trackerRows(
    slugs,
    contexts.map((q) => q.data?.trackers),
    runs.data?.rows,
  )
  const board = activeRepo(members, repo)

  return (
    <div className="space-y-6">
      <header className="space-y-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">{name}</h1>
          <span className="text-sm text-ink-2">
            {members.length === 1 ? '1 repo' : `group · ${members.length} repos`}
            {lastActivity && ` · last activity ${lastActivity}`}
          </span>
        </div>
        {members.length > 1 && (
          <ul aria-label="Repos" className="flex flex-wrap gap-1.5 text-xs">
            {members.map((m) => (
              <li key={m.slug} className="pill max-w-[22rem] gap-1.5" title={m.stack}>
                <Link
                  to="/g/$group"
                  params={{ group }}
                  search={{ tab: 'board', repo: m.slug }}
                  className="font-mono hover:underline"
                >
                  {m.slug}
                </Link>
                {m.stack && <span className="truncate text-ink-3"> · {m.stack}</span>}
              </li>
            ))}
          </ul>
        )}
        <nav
          aria-label="Tabs"
          role="tablist"
          className="flex items-end gap-0 border-b-2 border-ink text-sm"
        >
          {GROUP_TABS.map((t) => (
            <Link
              key={t}
              role="tab"
              aria-selected={t === tab}
              to="/g/$group"
              params={{ group }}
              search={{ tab: t, repo }}
              className={cn(
                '-mb-0.5 border-b-2 px-3 py-1.5 capitalize transition-colors',
                t === tab
                  ? 'display border-ink text-base text-ink'
                  : 'border-transparent text-ink-2 hover:text-ink',
              )}
            >
              {t}
            </Link>
          ))}
          <span className="ml-auto pb-1.5 text-xs text-ink-3">
            <kbd>[</kbd> <kbd>]</kbd>
          </span>
        </nav>
      </header>

      {tab === 'overview' && (
        <div className="space-y-8">
          <LeftOff members={members} onEdit={editNotes} />
          {attention.isError && <p className="text-crit">error: {attention.error.message}</p>}
          <div className="grid gap-8 wide:grid-cols-2">
            {needsMe && (
              <AttentionSection
                section={needsMe}
                capped={capSection(needsMe, true)}
                expanded
                onToggle={() => {}}
                onAction={onAction}
                onDismissRows={onDismissRows}
                onRestore={() => rows.mutate({ kind: 'restore', section: needsMe.name })}
              />
            )}
            {waiting && (
              <AttentionSection
                section={waiting}
                capped={capSection(waiting, true)}
                expanded
                onToggle={() => {}}
                onAction={onAction}
              />
            )}
          </div>
          <DoingList
            rows={doingRows}
            total={doing.length}
            showProject={members.length > 1}
            idleDays={idleDays}
          />
          <TrackerTable rows={trackerList} showProject={members.length > 1} />
        </div>
      )}

      {tab === 'board' && (
        <div className="space-y-4">
          {members.length > 1 && (
            <nav aria-label="Repo" className="flex flex-wrap gap-1 text-sm">
              {members.map((m) => (
                <Link
                  key={m.slug}
                  to="/g/$group"
                  params={{ group }}
                  search={{ tab: 'board', repo: m.slug }}
                  className="pill font-mono"
                  data-on={m.slug === board}
                  activeOptions={{ includeSearch: true }}
                >
                  {m.slug}
                </Link>
              ))}
            </nav>
          )}
          {board && <BoardPane slug={board} />}
        </div>
      )}

      {tab === 'runs' && <RunsPage projects={slugs} heading="" />}

      {tab === 'changes' && (
        <>
          {changes.isPending && <p className="text-ink-3">loading…</p>}
          {changes.isError && <p className="text-crit">error: {changes.error.message}</p>}
          {changes.data && (
            <ChangesList
              events={filterChanges(changes.data.events, group)}
              cutoff={changes.data.cutoff}
              timeText={(ts) => `${relativeTime(ts)} ${clockLabel(ts)}`.trim()}
            />
          )}
        </>
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
