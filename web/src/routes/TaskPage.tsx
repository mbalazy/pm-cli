import { useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'

import { useProjects, useTask } from '../api/queries'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { TaskDetail } from '../components/TaskDetail'
import { Toast } from '../components/Toast'
import { useConfirmedAction } from '../hooks/useConfirmedAction'
import { useRecentTasks } from '../hooks/useRecentTasks'
import { useShortcuts } from '../hooks/useShortcuts'
import { subjectOfTask } from '../lib/confirmText'
import { relativeTime } from '../lib/relativeTime'
import { splitSpecLog } from '../lib/splitSpecLog'

export function TaskPage({ slug, id }: { slug: string; id: string }) {
  const task = useTask(slug, id)
  const projects = useProjects()
  const action = useConfirmedAction()
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
  const t = task.data
  const statuses = projects.data?.projects.find((p) => p.slug === slug)?.statuses ?? []
  return (
    <>
      <TaskDetail
        task={t}
        updatedText={relativeTime(t.updated)}
        statusChangedText={relativeTime(t.status_changed ?? '')}
        spec={spec}
        log={log}
        onBack={back}
        statuses={statuses}
        onEdit={(kind, status) => action.ask({ kind, subject: subjectOfTask(t), status })}
      />
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
    </>
  )
}
