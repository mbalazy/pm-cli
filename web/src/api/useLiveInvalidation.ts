import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import { keys } from './queries'

// /api/events says WHAT changed (`tasks`/`runs` + the project slug), never
// what to; the client answers by invalidating the queries that read it, and
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
      // The sidebar counts live here and move with every task edit.
      void client.invalidateQueries({ queryKey: keys.projects() })
    })
    es.addEventListener('runs', () => {
      void client.invalidateQueries({ queryKey: keys.runs() })
    })
    return () => es.close()
  }, [client, url])
}
