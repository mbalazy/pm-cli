import { useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'

import { useTask } from '../api/queries'
import { TaskDetail } from '../components/TaskDetail'
import { useRecentTasks } from '../hooks/useRecentTasks'
import { useShortcuts } from '../hooks/useShortcuts'
import { relativeTime } from '../lib/relativeTime'
import { splitSpecLog } from '../lib/splitSpecLog'

export function TaskPage({ slug, id }: { slug: string; id: string }) {
  const task = useTask(slug, id)
  const navigate = useNavigate()
  const { push } = useRecentTasks()
  const back = () => void navigate({ to: '/p/$slug', params: { slug } })
  useShortcuts({ close: back })

  const loaded = task.data
  useEffect(() => {
    if (loaded) push({ project: loaded.project, id: loaded.id, title: loaded.title })
  }, [loaded, push])

  if (task.isPending) return <p>loading…</p>
  if (task.isError) return <p className="text-red-700">error: {task.error.message}</p>

  const { spec, log } = splitSpecLog(task.data.body ?? '')
  return (
    <TaskDetail
      task={task.data}
      updatedText={relativeTime(task.data.updated)}
      statusChangedText={relativeTime(task.data.status_changed ?? '')}
      spec={spec}
      log={log}
      onBack={back}
    />
  )
}
