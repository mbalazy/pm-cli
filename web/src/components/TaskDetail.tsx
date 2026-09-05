import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { TaskDetail as TaskDetailDto } from '../api/types'

// Presentation only. The relative times and the Spec/Log split arrive as
// props: "how long ago" needs a clock and the split is a lib rule, neither
// belongs in a component. The edits (brief, waiting reason, status) only
// ASK - the route opens the confirmation dialog and sends the request.

interface Props {
  task: TaskDetailDto
  updatedText: string
  /** Empty when the stamp is unknown (older task) - rendered as such. */
  statusChangedText: string
  spec: string | null
  log: string
  onBack?: () => void
  /** The project's status list, for the status select. */
  statuses?: string[]
  /** Asks to edit a field; the status edit names the chosen status. */
  onEdit?: (kind: 'edit_brief' | 'edit_waiting_for' | 'set_status', status?: string) => void
}

export function TaskDetail({
  task,
  updatedText,
  statusChangedText,
  spec,
  log,
  onBack,
  statuses = [],
  onEdit,
}: Props) {
  const editBtn = 'ml-2 rounded border px-1 text-xs font-normal'
  return (
    <article className="space-y-4">
      {onBack && (
        <button type="button" onClick={onBack} className="text-sm underline md:hidden">
          ← back to list
        </button>
      )}
      <header>
        <h1 className="text-xl font-bold">{task.title}</h1>
        <p className="flex flex-wrap items-baseline gap-x-1 text-sm text-gray-600">
          <code>#{task.id}</code> · {task.project} ·{' '}
          {onEdit && statuses.length > 0 ? (
            <label>
              <span className="sr-only">status</span>
              <select
                aria-label="status"
                value={task.status}
                onChange={(e) => onEdit('set_status', e.target.value)}
                className="rounded border px-1 font-bold"
              >
                {[...statuses, ...(statuses.includes(task.status) ? [] : [task.status])].map(
                  (s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ),
                )}
              </select>
            </label>
          ) : (
            <b>{task.status}</b>
          )}
          {task.waiting_for && <> · waiting for: {task.waiting_for}</>}
          {onEdit && (
            <button type="button" className={editBtn} onClick={() => onEdit('edit_waiting_for')}>
              {task.waiting_for ? 'edit reason' : 'set reason'}
            </button>
          )}
        </p>
        <p className="text-sm text-gray-600">
          updated {updatedText} · status changed {statusChangedText || 'unknown'}
        </p>
      </header>

      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
        {task.branch && (
          <>
            <dt className="text-gray-500">branch</dt>
            <dd>
              <code>{task.branch}</code>
            </dd>
          </>
        )}
        {task.parent && (
          <>
            <dt className="text-gray-500">parent</dt>
            <dd>
              <code>{task.parent}</code>
            </dd>
          </>
        )}
        {task.tags && task.tags.length > 0 && (
          <>
            <dt className="text-gray-500">tags</dt>
            <dd>{task.tags.join(', ')}</dd>
          </>
        )}
        {task.links && Object.keys(task.links).length > 0 && (
          <>
            <dt className="text-gray-500">links</dt>
            <dd className="space-x-2">
              {Object.entries(task.links).map(([name, url]) => (
                <a key={name} href={url} className="underline" target="_blank" rel="noreferrer">
                  {name}
                </a>
              ))}
            </dd>
          </>
        )}
        {task.depends_on && task.depends_on.length > 0 && (
          <>
            <dt className="text-gray-500">depends on</dt>
            <dd>{task.depends_on.join(', ')}</dd>
          </>
        )}
        {task.mode && (
          <>
            <dt className="text-gray-500">mode</dt>
            <dd>{task.mode}</dd>
          </>
        )}
      </dl>

      {task.ac && (
        <section>
          <h2 className="font-semibold">Acceptance criteria</h2>
          <p className="whitespace-pre-wrap text-sm">{task.ac}</p>
        </section>
      )}
      {(task.brief || onEdit) && (
        <section>
          <h2 className="font-semibold">
            Brief
            {onEdit && (
              <button type="button" className={editBtn} onClick={() => onEdit('edit_brief')}>
                {task.brief ? 'edit brief' : 'write brief'}
              </button>
            )}
          </h2>
          {task.brief && (
            <div className="markdown text-sm">
              <Markdown remarkPlugins={[remarkGfm]}>{task.brief}</Markdown>
            </div>
          )}
        </section>
      )}
      {spec !== null && (
        <section>
          <h2 className="font-semibold">Spec (current truth)</h2>
          <div className="markdown text-sm">
            <Markdown remarkPlugins={[remarkGfm]}>{spec}</Markdown>
          </div>
        </section>
      )}
      {log !== '' && (
        <section>
          <h2 className="font-semibold">Log (history, append-only)</h2>
          <div className="markdown text-sm">
            <Markdown remarkPlugins={[remarkGfm]}>{log}</Markdown>
          </div>
        </section>
      )}
    </article>
  )
}
