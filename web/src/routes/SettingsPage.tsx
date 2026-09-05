import { useEffect, useState } from 'react'

import { useRowMutation } from '../api/mutations'
import { useConfig, useGroups, useProjects } from '../api/queries'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { SettingsGroup } from '../components/SettingsGroup'
import { Toast } from '../components/Toast'
import { useConfirmedAction } from '../hooks/useConfirmedAction'
import { SECTION_ORDER, sectionMeta } from '../lib/attentionView'
import {
  NEEDS_ME_ORDER,
  SIDEBAR_SORTS,
  SIDEBAR_VARIANTS,
  SOURCE_ORDER,
  formFromConfig,
  groupRows,
  patchFromForm,
  slackMappingOf,
  slackRows,
  sleepRows,
  type SettingsForm,
  type SlackRow,
} from '../lib/settingsView'

// The Settings screen: the cockpit block of config.yaml as one form (every
// group of the wireframe, saved as a PATCH of what changed), and the
// per-project settings (group, Slack mapping, asleep) written to each
// project.yaml. Sleeping, waking and moving a repo go through the dialog -
// they change what the home screen shows; the form's Save and a Slack
// row's save are explicit buttons and post directly. Composition only: the
// form/patch/rows rules are lib/settingsView's, validation is the server's.

export function SettingsPage() {
  const config = useConfig()
  const projects = useProjects()
  const groups = useGroups()
  const action = useConfirmedAction()
  const save = useRowMutation()
  const [form, setForm] = useState<SettingsForm>()
  const [message, setMessage] = useState('')

  const cockpit = config.data?.cockpit
  const base = cockpit ? formFromConfig(cockpit) : undefined
  const dirty = base !== undefined && form !== undefined && patchFromForm(base, form) !== null
  useEffect(() => {
    // Take the server's values whenever they change, unless the user is
    // mid-edit - a save elsewhere must not wipe a half-typed form.
    if (cockpit && !dirty) setForm(formFromConfig(cockpit))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cockpit])

  const notify = (m: string) => {
    setMessage(m)
    setTimeout(() => setMessage(''), 3000)
  }
  const submit = () => {
    if (!base || !form) return
    const patch = patchFromForm(base, form)
    if (!patch) return notify('nothing changed')
    save.mutate({ kind: 'settings', body: patch }, { onSuccess: () => notify('saved') })
  }
  const saveSlack = (row: SlackRow) =>
    save.mutate(
      { kind: 'project', project: row.slug, body: { slack: slackMappingOf(row) } },
      { onSuccess: () => notify(`saved ${row.slug}`) },
    )

  if (config.isPending) return <p className="text-ink-3">loading…</p>
  if (config.isError) return <p className="text-crit">config: {config.error.message}</p>
  if (!form || !cockpit) return null
  const set = (patch: Partial<SettingsForm>) => setForm({ ...form, ...patch })
  const rows = groupRows(groups.data?.groups, form, cockpit.groups)
  const groupSlugs = rows.map((r) => r.slug)
  const { asleep, awake } = sleepRows(projects.data?.projects)
  const num = (v: string, fallback: number) => (v === '' ? fallback : Number(v))

  return (
    <div className="space-y-2">
      <header className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-b-2 border-ink pb-3">
        <h1 className="masthead">Settings</h1>
        <span className="text-sm text-ink-2">
          saved to &lt;pm-root&gt;/config.yaml (global) and project.yaml (per project)
        </span>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
        aria-label="Cockpit settings"
      >
        <SettingsGroup
          label="Sources"
          note="which sources the change feed runs; each has its own switch"
        >
          <div className="flex flex-wrap gap-3">
            {SOURCE_ORDER.map((s) => (
              <label key={s.name} className="flex items-center gap-1">
                <input
                  type="checkbox"
                  className="accent-ink"
                  checked={form.sources[s.name] === true}
                  onChange={(e) =>
                    set({ sources: { ...form.sources, [s.name]: e.target.checked } })
                  }
                />
                {s.label}
              </label>
            ))}
          </div>
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              className="accent-ink"
              checked={form.git_all_branches}
              onChange={(e) => set({ git_all_branches: e.target.checked })}
            />
            git: scan every branch (default: only branches pm tasks name + open PRs)
          </label>
          <div className="flex flex-wrap items-center gap-2">
            report model
            <input
              type="text"
              aria-label="report model"
              value={form.report_model}
              onChange={(e) => set({ report_model: e.target.value })}
              className="field w-24"
            />
            language
            <input
              type="text"
              aria-label="report language"
              value={form.report_language}
              onChange={(e) => set({ report_language: e.target.value })}
              className="field w-14"
            />
            <span className="text-xs text-ink-2">
              the LLM report costs tokens: one per period, written after the first refresh
            </span>
          </div>
        </SettingsGroup>

        <SettingsGroup
          label="Cutoff"
          note='"since yesterday" starts at this hour - fixed, not the last 24 h'
        >
          <label className="flex items-center gap-2">
            <input
              type="number"
              min={0}
              max={23}
              aria-label="cutoff hour"
              value={form.cutoff_hour}
              onChange={(e) => set({ cutoff_hour: num(e.target.value, 0) })}
              className="field w-16"
            />
            :00
          </label>
        </SettingsGroup>

        <SettingsGroup
          label="Refresh"
          note="outside the window only a manual refresh runs; 0 minutes = never automatically"
        >
          <div className="flex flex-wrap items-center gap-2">
            every
            <input
              type="number"
              min={0}
              aria-label="refresh every minutes"
              value={form.refresh_minutes}
              onChange={(e) => set({ refresh_minutes: num(e.target.value, 0) })}
              className="field w-16"
            />
            min, within
            <input
              type="text"
              aria-label="refresh window"
              value={form.window}
              onChange={(e) => set({ window: e.target.value })}
              placeholder="07:00-20:00"
              className="field w-28"
            />
          </div>
        </SettingsGroup>

        <SettingsGroup label="Thresholds" note="days; v1 values, to be tuned">
          <div className="flex flex-wrap items-center gap-3">
            {(
              [
                ['doing_idle_days', 'doing idle'],
                ['waiting_highlight_days', 'waiting highlighted after'],
                ['stuck_project_days', 'project stuck after'],
              ] as const
            ).map(([key, label]) => (
              <label key={key} className="flex items-center gap-1">
                {label}
                <input
                  type="number"
                  min={1}
                  aria-label={label}
                  value={form[key]}
                  onChange={(e) => set({ [key]: num(e.target.value, 1) })}
                  className="field w-16"
                />
                d
              </label>
            ))}
          </div>
        </SettingsGroup>

        <SettingsGroup
          label='"Needs me" order'
          note="fixed in v1 - the aggregation's rule, not a setting"
        >
          <span className="chip">{NEEDS_ME_ORDER}</span>
        </SettingsGroup>

        <SettingsGroup label="Home sections" note="in the order the home screen shows them">
          <div className="flex flex-wrap gap-3">
            {SECTION_ORDER.map((name) => (
              <label key={name} className="flex items-center gap-1">
                <input
                  type="checkbox"
                  className="accent-ink"
                  checked={form.sections[name] === true}
                  onChange={(e) =>
                    set({ sections: { ...form.sections, [name]: e.target.checked } })
                  }
                />
                {sectionMeta(name).title}
              </label>
            ))}
          </div>
        </SettingsGroup>

        <SettingsGroup label="Sidebar">
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-1">
              variant
              <select
                aria-label="sidebar variant"
                value={form.sidebar.variant}
                onChange={(e) => set({ sidebar: { ...form.sidebar, variant: e.target.value } })}
                className="field"
              >
                {SIDEBAR_VARIANTS.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex items-center gap-1">
              <input
                type="checkbox"
                className="accent-ink"
                checked={form.sidebar.show_repos}
                onChange={(e) =>
                  set({ sidebar: { ...form.sidebar, show_repos: e.target.checked } })
                }
              />
              repos under a group
            </label>
            <label className="flex items-center gap-1">
              sort
              <select
                aria-label="sidebar sort"
                value={form.sidebar.sort}
                onChange={(e) => set({ sidebar: { ...form.sidebar, sort: e.target.value } })}
                className="field"
              >
                {SIDEBAR_SORTS.map((v) => (
                  <option key={v} value={v}>
                    {v.replace('_', ' ')}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex items-center gap-1">
              width
              <input
                type="number"
                min={0}
                aria-label="sidebar width"
                value={form.sidebar.width}
                onChange={(e) =>
                  set({ sidebar: { ...form.sidebar, width: num(e.target.value, 0) } })
                }
                className="field w-16"
              />
              px (0 = default)
            </label>
          </div>
        </SettingsGroup>

        <SettingsGroup
          label="Project groups"
          note="name and manual order live in config.yaml (Save above); moving a repo writes group: into its project.yaml"
        >
          <table className="ledger-table">
            <thead className="text-xs text-ink-2">
              <tr>
                <th>group</th>
                <th>name</th>
                <th>order</th>
                <th>repos</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.slug} className="align-top">
                  <td className="id text-ink">{r.slug}</td>
                  <td>
                    <input
                      type="text"
                      aria-label={`name of ${r.slug}`}
                      value={form.groups[r.slug]?.name ?? ''}
                      placeholder={r.slug}
                      onChange={(e) =>
                        set({
                          groups: {
                            ...form.groups,
                            [r.slug]: {
                              name: e.target.value,
                              order: form.groups[r.slug]?.order ?? 0,
                            },
                          },
                        })
                      }
                      className="field w-40"
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      aria-label={`order of ${r.slug}`}
                      value={form.groups[r.slug]?.order ?? 0}
                      onChange={(e) =>
                        set({
                          groups: {
                            ...form.groups,
                            [r.slug]: {
                              name: form.groups[r.slug]?.name ?? '',
                              order: num(e.target.value, 0),
                            },
                          },
                        })
                      }
                      className="field w-14"
                    />
                  </td>
                  <td>
                    <ul className="flex flex-wrap gap-2">
                      {r.members.map((slug) => (
                        <li key={slug} className="flex items-center gap-1">
                          <span className="id text-ink">{slug}</span>
                          <select
                            aria-label={`group of ${slug}`}
                            value={r.slug}
                            onChange={(e) =>
                              action.ask({
                                kind: 'move_repo',
                                subject: {
                                  project: slug,
                                  title: slug,
                                  group: e.target.value === slug ? '' : e.target.value,
                                },
                              })
                            }
                            className="field h-6 min-h-0 py-0 text-xs"
                          >
                            {groupSlugs.map((g) => (
                              <option key={g} value={g}>
                                {g === slug ? `${g} (own)` : g}
                              </option>
                            ))}
                            {!groupSlugs.includes(slug) && (
                              <option value={slug}>{slug} (own)</option>
                            )}
                          </select>
                        </li>
                      ))}
                      {r.members.length === 0 && (
                        <li className="text-xs text-ink-3">no active repo</li>
                      )}
                    </ul>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </SettingsGroup>

        <div className="flex items-center gap-3 py-3">
          <button
            type="submit"
            disabled={save.isPending || !dirty}
            className="rounded-sm bg-ink px-4 py-1 text-sm font-medium text-paper transition-opacity hover:opacity-90 disabled:opacity-40"
          >
            {save.isPending ? 'saving…' : 'Save'}
          </button>
          {dirty && <span className="text-xs text-warn">unsaved changes</span>}
          {save.isError && <span className="text-sm text-crit">error: {save.error.message}</span>}
        </div>
      </form>

      <SettingsGroup
        label="Slack per project"
        note="workspace label + channels whose messages count as the project's changes; mentions and DMs are always in the feed, project-less"
      >
        <p className="text-xs text-ink-2">
          workspaces with an MCP server (cockpit.slack.servers in config.yaml, hand-edited):{' '}
          {cockpit.slack.workspaces.length === 0
            ? 'none - the slack source has nothing to run'
            : cockpit.slack.workspaces
                .map((w) => `${w.workspace} (${w.source}${w.has_me ? '' : ', no me'})`)
                .join(' · ')}
        </p>
        <table className="ledger-table">
          <tbody>
            {slackRows(projects.data?.projects).map((r) => (
              <SlackRowEditor
                key={`${r.slug}:${r.workspace}:${r.channels}`}
                row={r}
                onSave={saveSlack}
              />
            ))}
          </tbody>
        </table>
      </SettingsGroup>

      <SettingsGroup
        label="Asleep projects"
        note="archived: true in project.yaml - out of every group, the home queue, the sidebar and the feed; the TUI board hides on its own"
      >
        <ul className="flex flex-wrap gap-2">
          {asleep.map((p) => (
            <li key={p.slug} className="pill gap-2">
              <span className="font-mono">{p.slug}</span>
              <button
                type="button"
                className="text-xs underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                onClick={() =>
                  action.ask({ kind: 'wake_project', subject: { project: p.slug, title: p.name } })
                }
              >
                wake
              </button>
            </li>
          ))}
          {asleep.length === 0 && <li className="text-xs text-ink-3">none asleep</li>}
        </ul>
        <details>
          <summary className="cursor-pointer text-xs text-ink-2">put a project to sleep…</summary>
          <ul className="mt-1 flex flex-wrap gap-2">
            {awake.map((p) => (
              <li key={p.slug} className="pill gap-2">
                <span className="font-mono">{p.slug}</span>
                <button
                  type="button"
                  className="text-xs underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                  onClick={() =>
                    action.ask({
                      kind: 'sleep_project',
                      subject: { project: p.slug, title: p.name },
                    })
                  }
                >
                  sleep
                </button>
              </li>
            ))}
          </ul>
        </details>
      </SettingsGroup>

      <SettingsGroup
        label="Remote pm"
        note="runs only, on demand (the Runs screen's fetch remote); full projects from a VPS: later"
      >
        <span className="chip">remotes: in config.yaml, hand-edited (pm config show)</span>
      </SettingsGroup>

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
      <Toast message={message || action.toast.message} error={!message && action.toast.error} />
    </div>
  )
}

function SlackRowEditor({ row, onSave }: { row: SlackRow; onSave: (r: SlackRow) => void }) {
  // Keyed on the server's values by the parent, so a save elsewhere remounts it.
  const [edit, setEdit] = useState(row)
  const dirty = edit.workspace !== row.workspace || edit.channels !== row.channels
  return (
    <tr>
      <td className="id text-ink">{row.slug}</td>
      <td>
        <input
          type="text"
          aria-label={`slack workspace of ${row.slug}`}
          value={edit.workspace}
          placeholder="workspace"
          onChange={(e) => setEdit({ ...edit, workspace: e.target.value })}
          className="field w-28"
        />
      </td>
      <td>
        <input
          type="text"
          aria-label={`slack channels of ${row.slug}`}
          value={edit.channels}
          placeholder="#channel, #other"
          onChange={(e) => setEdit({ ...edit, channels: e.target.value })}
          className="field w-full"
        />
      </td>
      <td>
        <button type="button" disabled={!dirty} className="ghost-btn" onClick={() => onSave(edit)}>
          save
        </button>
      </td>
    </tr>
  )
}
