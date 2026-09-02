import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { TaskDetail as TaskDetailDto } from '../api/types'

// Presentation only. The relative times and the Spec/Log split arrive as
// props: "how long ago" needs a clock and the split is a lib rule, neither
// belongs in a component.

interface Props {
  task: TaskDetailDto
  updatedText: string
  /** Empty when the stamp is unknown (older task) - rendered as such. */
  statusChangedText: string
  spec: string | null
  log: string
  onBack?: () => void
}

export function TaskDetail({ task, updatedText, statusChangedText, spec, log, onBack }: Props) {
  return (
    <article className="space-y-4">
      {onBack && (
        <button type="button" onClick={onBack} className="text-sm underline md:hidden">
          ← back to list
        </button>
      )}
      <header>
        <h1 className="text-xl font-bold">{task.title}</h1>
        <p className="text-sm text-gray-600">
          <code>#{task.id}</code> · {task.project} · <b>{task.status}</b>
          {task.waiting_for && <> · waiting for: {task.waiting_for}</>}
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
      {task.brief && (
        <section>
          <h2 className="font-semibold">Brief</h2>
          <div className="markdown text-sm">
            <Markdown remarkPlugins={[remarkGfm]}>{task.brief}</Markdown>
          </div>
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
