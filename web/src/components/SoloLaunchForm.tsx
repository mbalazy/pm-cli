import { Rocket } from 'lucide-react'
import { useState } from 'react'

import type { Project, SoloInput } from '../api/types'
import { SOLO_RUNTIMES, soloFormDefaults, soloInputOf } from '../lib/soloView'

// The "launch solo" form (pm-cli-141): project, the queue (/solo's own
// argument), the runtime, base, model, push/pr and the two caps. Submit hands
// the input UP - the page opens the confirmation dialog with the server's
// command preview; nothing here posts. Props in, JSX out.

interface Props {
  /** The projects offered (active ones); the first is preselected unless `project` says otherwise. */
  projects: Project[]
  /** The project to preselect (a group's board repo). */
  project?: string
  onSubmit: (input: SoloInput) => void
}

export function SoloLaunchForm({ projects, project, onSubmit }: Props) {
  const [form, setForm] = useState(() => soloFormDefaults(project ?? projects[0]?.slug ?? ''))
  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [k]: v }))
  const ready = form.project !== '' && form.queue.trim() !== ''
  return (
    <form
      aria-label="Launch solo"
      className="space-y-3 rounded-sm border border-rule bg-paper-2 p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault()
        if (ready) onSubmit(soloInputOf(form))
      }}
    >
      <div className="grid gap-3 sm:grid-cols-[minmax(8rem,12rem)_1fr]">
        <label className="block">
          <span className="kicker">project</span>
          <select
            className="field mt-1 w-full"
            value={form.project}
            onChange={(e) => set('project', e.target.value)}
          >
            {projects.map((p) => (
              <option key={p.slug} value={p.slug}>
                {p.slug}
              </option>
            ))}
          </select>
        </label>
        <label className="block">
          <span className="kicker">queue</span>
          <input
            className="field mt-1 w-full"
            value={form.queue}
            onChange={(e) => set('queue', e.target.value)}
            placeholder="a tracker id, task ids, a ticket key, a link, or a sentence"
          />
        </label>
      </div>
      <div className="grid gap-3 sm:grid-cols-4">
        <label className="block">
          <span className="kicker">runtime</span>
          <select
            className="field mt-1 w-full"
            value={form.runtime}
            onChange={(e) => set('runtime', e.target.value)}
          >
            {SOLO_RUNTIMES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <label className="block">
          <span className="kicker">base branch</span>
          <input
            className="field mt-1 w-full"
            value={form.base}
            onChange={(e) => set('base', e.target.value)}
            placeholder="the skill's default"
          />
        </label>
        <label className="block">
          <span className="kicker">model</span>
          <input
            className="field mt-1 w-full"
            value={form.model}
            onChange={(e) => set('model', e.target.value)}
            placeholder="account default"
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="block">
            <span className="kicker">max tasks</span>
            <input
              className="field mt-1 w-full"
              inputMode="numeric"
              value={form.max_tasks}
              onChange={(e) => set('max_tasks', e.target.value)}
              placeholder="all"
            />
          </label>
          <label className="block">
            <span className="kicker">max hours</span>
            <input
              className="field mt-1 w-full"
              inputMode="numeric"
              value={form.max_hours}
              onChange={(e) => set('max_hours', e.target.value)}
              placeholder="4"
            />
          </label>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            className="accent-ink"
            checked={form.push}
            onChange={(e) => set('push', e.target.checked)}
          />
          push branches (--push)
        </label>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            className="accent-ink"
            checked={form.pr}
            onChange={(e) => set('pr', e.target.checked)}
          />
          open PRs (--pr)
        </label>
        <button
          type="submit"
          disabled={!ready}
          className="ml-auto flex items-center gap-1.5 rounded-sm bg-ink px-3 py-1 font-medium text-paper transition-opacity hover:opacity-90 disabled:opacity-50"
        >
          <Rocket aria-hidden="true" className="size-3.5" strokeWidth={1.75} />
          launch solo…
        </button>
      </div>
    </form>
  )
}
