import { ArrowLeft, ExternalLink } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { TaskDetail as TaskDetailDto } from '../api/types'
import { SectionHead } from './SectionHead'

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
  return (
    <article className="space-y-6">
      {onBack && (
        <button type="button" onClick={onBack} className="ghost-btn md:hidden">
          <ArrowLeft aria-hidden="true" className="size-3" />← back to list
        </button>
      )}
      <header className="space-y-2 border-b-2 border-ink pb-3">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-ink-2">
          <code className="id">#{task.id}</code>
          <span aria-hidden="true">·</span>
          <span className="chip">{task.project}</span>
          <span aria-hidden="true">·</span>
          {onEdit && statuses.length > 0 ? (
            <label>
              <span className="sr-only">status</span>
              <select
                aria-label="status"
                value={task.status}
                onChange={(e) => onEdit('set_status', e.target.value)}
                className="field h-7 py-0 font-medium text-ink"
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
            <b className="text-ink">{task.status}</b>
          )}
          {task.waiting_for && <span>· waiting for: {task.waiting_for}</span>}
          {onEdit && (
            <button type="button" className="ghost-btn" onClick={() => onEdit('edit_waiting_for')}>
              {task.waiting_for ? 'edit reason' : 'set reason'}
            </button>
          )}
        </p>
        <h1 className="masthead">{task.title}</h1>
        <p className="num text-ink-3">
          updated {updatedText} · status changed {statusChangedText || 'unknown'}
        </p>
      </header>

      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        {task.branch && (
          <>
            <dt className="kicker pt-0.5">branch</dt>
            <dd>
              <code className="id text-ink">{task.branch}</code>
            </dd>
          </>
        )}
        {task.parent && (
          <>
            <dt className="kicker pt-0.5">parent</dt>
            <dd>
              <code className="id text-ink">{task.parent}</code>
            </dd>
          </>
        )}
        {task.tags && task.tags.length > 0 && (
          <>
            <dt className="kicker pt-0.5">tags</dt>
            <dd className="flex flex-wrap gap-1">
              {task.tags.map((t) => (
                <span key={t} className="chip">
                  {t}
                </span>
              ))}
            </dd>
          </>
        )}
        {task.links && Object.keys(task.links).length > 0 && (
          <>
            <dt className="kicker pt-0.5">links</dt>
            <dd className="flex flex-wrap gap-x-3 gap-y-1">
              {Object.entries(task.links).map(([name, url]) => (
                <a
                  key={name}
                  href={url}
                  className="inline-flex items-center gap-1 underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                  target="_blank"
                  rel="noreferrer"
                >
                  {name}
                  <ExternalLink aria-hidden="true" className="size-3 text-ink-3" />
                </a>
              ))}
            </dd>
          </>
        )}
        {task.depends_on && task.depends_on.length > 0 && (
          <>
            <dt className="kicker pt-0.5">depends on</dt>
            <dd className="id text-ink">{task.depends_on.join(', ')}</dd>
          </>
        )}
        {task.mode && (
          <>
            <dt className="kicker pt-0.5">mode</dt>
            <dd>{task.mode}</dd>
          </>
        )}
      </dl>

      {task.ac && (
        <section>
          <SectionHead title="Acceptance criteria" />
          <p className="max-w-[72ch] text-sm whitespace-pre-wrap">{task.ac}</p>
        </section>
      )}
      {(task.brief || onEdit) && (
        <section>
          <SectionHead title="Brief">
            {onEdit && (
              <button type="button" className="ghost-btn" onClick={() => onEdit('edit_brief')}>
                {task.brief ? 'edit brief' : 'write brief'}
              </button>
            )}
          </SectionHead>
          {task.brief && (
            <div className="markdown">
              <Markdown remarkPlugins={[remarkGfm]}>{task.brief}</Markdown>
            </div>
          )}
        </section>
      )}
      {spec !== null && (
        <section>
          <SectionHead title="Spec (current truth)" />
          <div className="markdown">
            <Markdown remarkPlugins={[remarkGfm]}>{spec}</Markdown>
          </div>
        </section>
      )}
      {log !== '' && (
        <section>
          <SectionHead title="Log (history, append-only)" />
          <div className="markdown">
            <Markdown remarkPlugins={[remarkGfm]}>{log}</Markdown>
          </div>
        </section>
      )}
    </article>
  )
}
