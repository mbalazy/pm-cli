import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import { keys } from './queries'

// /api/events says WHAT changed (`tasks`/`runs` + the project slug, or
// `changes` after the feed refreshed, `settings` after config.yaml was
// written), never what to; the client answers by invalidating the queries that read it, and
// react-query refetches the ones on screen. EventSource reconnects on its
// own after a drop - nothing here retries.

/** The browser's EventSource, or whatever a test stubs in. */
type EventSourceLike = Pick<EventSource, 'addEventListener' | 'close'>

interface ChangeData {
  project?: string
}

function parseProject(raw: string): string | undefined {
  try {
    return (JSON.parse(raw) as ChangeData).project
  } catch {
    return undefined
  }
}

export function useLiveInvalidation(url = '/api/events') {
  const client = useQueryClient()
  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const es: EventSourceLike = new EventSource(url)
    es.addEventListener('tasks', (e) => {
      const slug = parseProject((e as MessageEvent<string>).data)
      if (slug === undefined) return
      void client.invalidateQueries({ queryKey: keys.tasks(slug) })
      // ['task', slug] is a prefix of every ['task', slug, id].
      void client.invalidateQueries({ queryKey: ['task', slug] })
      void client.invalidateQueries({ queryKey: keys.context(slug) })
      // The sidebar counts live here and move with every task edit; the
      // groups move with a project.yaml edit, which the same event covers.
      void client.invalidateQueries({ queryKey: keys.projects() })
      void client.invalidateQueries({ queryKey: keys.groups() })
      // ['attention'] is a prefix of every scoped ['attention', p, g].
      void client.invalidateQueries({ queryKey: ['attention'] })
    })
    es.addEventListener('runs', () => {
      void client.invalidateQueries({ queryKey: keys.runs() })
      void client.invalidateQueries({ queryKey: ['attention'] })
    })
    es.addEventListener('changes', () => {
      void client.invalidateQueries({ queryKey: keys.changes() })
      void client.invalidateQueries({ queryKey: ['attention'] })
    })
    // The settings screen (or another tab of it) wrote config.yaml: the
    // sidebar shape, the sections and the thresholds are all downstream.
    es.addEventListener('settings', () => {
      void client.invalidateQueries({ queryKey: keys.config() })
      void client.invalidateQueries({ queryKey: ['attention'] })
      void client.invalidateQueries({ queryKey: keys.projects() })
      void client.invalidateQueries({ queryKey: keys.groups() })
    })
    return () => es.close()
  }, [client, url])
}
