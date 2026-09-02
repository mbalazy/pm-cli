import type { Project, TaskSummary } from '../api/types'

// What the ⌘K palette offers and how it filters. cmdk's own fuzzy filter is
// switched off so the rule lives here, testable: case-insensitive substring
// on a project's name/slug or a task's id/title; an empty query lists the
// projects, the fixed actions and the recently opened tasks.

export type PaletteItem =
  | { kind: 'project'; id: string; label: string; to: string }
  | { kind: 'task'; id: string; label: string; to: string }
  | { kind: 'runs'; id: 'runs'; label: string; to: string }

export interface RecentTask {
  project: string
  id: string
  title: string
}

export interface PaletteInput {
  projects: Project[]
  /** The active project's loaded tasks (searchable by id and title). */
  tasks: TaskSummary[]
  recent: RecentTask[]
  query: string
}

export function projectPath(slug: string): string {
  return `/p/${encodeURIComponent(slug)}`
}

export function taskPath(slug: string, id: string): string {
  return `${projectPath(slug)}/t/${encodeURIComponent(id)}`
}

export function paletteItems({ projects, tasks, recent, query }: PaletteInput): PaletteItem[] {
  const q = query.trim().toLowerCase()
  const has = (...fields: string[]) => q === '' || fields.some((f) => f.toLowerCase().includes(q))

  const out: PaletteItem[] = []
  for (const p of projects) {
    if (p.archived) continue
    if (has(p.name, p.slug)) {
      out.push({
        kind: 'project',
        id: p.slug,
        label: `go to project ${p.name}`,
        to: projectPath(p.slug),
      })
    }
  }
  if (has('runs')) out.push({ kind: 'runs', id: 'runs', label: 'runs', to: '/runs' })

  if (q === '') {
    for (const r of recent) {
      out.push({
        kind: 'task',
        id: r.id,
        label: `open task ${r.id} ${r.title}`,
        to: taskPath(r.project, r.id),
      })
    }
    return out
  }
  for (const t of tasks) {
    if (has(t.id, t.title)) {
      out.push({
        kind: 'task',
        id: t.id,
        label: `open task ${t.id} ${t.title}`,
        to: taskPath(t.project, t.id),
      })
    }
  }
  return out
}
