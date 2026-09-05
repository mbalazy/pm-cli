import { Link } from '@tanstack/react-router'

import type { ReportSuggestion } from '../api/types'
import { canDo, type ReportView } from '../lib/reportView'

// The LLM report over the feed (pm-cli-118-20): the period's prose, the
// cost in small print, and the open suggestions - each with "do" (the
// mapped action, through the page's dialog) and "dismiss". The state and
// the wording are lib/reportView's; the page owns the writing and the
// dismissing.

interface Props {
  view: ReportView
  onWrite: () => void
  onDo: (s: ReportSuggestion) => void
  onDismiss: (s: ReportSuggestion) => void
}

export function ReportPanel({ view, onWrite, onDo, onDismiss }: Props) {
  return (
    <section aria-label="Report" data-state={view.state} className="rounded border p-3 text-sm">
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2">
        <span className="font-semibold">Report</span>
        <span className="text-xs text-gray-400">
          written by an LLM from the raw feed below, one per period · optional, costs tokens
        </span>
        {view.canWrite && (
          <button type="button" className="ml-auto rounded border px-2 text-xs" onClick={onWrite}>
            {view.state === 'done' || view.state === 'error' ? 'write again' : 'write now'}
          </button>
        )}
      </h2>
      <p className="text-xs text-gray-500">
        {view.status}
        {view.cost && <span className="ml-2 text-gray-400">· {view.cost}</span>}
      </p>
      {view.error && (
        <p role="alert" className="mt-1 text-red-700">
          {view.error}
        </p>
      )}
      {view.paragraphs.length > 0 && (
        <div className="mt-2 space-y-2">
          {view.paragraphs.map((p, i) => (
            <p key={i} className="whitespace-pre-wrap">
              {p}
            </p>
          ))}
        </div>
      )}
      {(view.open.length > 0 || view.dismissedCount > 0) && (
        <div className="mt-2">
          <h3 className="text-xs uppercase text-gray-500">
            Suggestions
            {view.dismissedCount > 0 && (
              <span className="ml-1 normal-case text-gray-400">
                ({view.dismissedCount} dismissed)
              </span>
            )}
          </h3>
          <ul className="space-y-0.5">
            {view.open.map((s) => (
              <li key={s.id} className="flex flex-wrap items-baseline gap-2">
                {s.project ? (
                  <Link
                    to="/p/$slug/t/$id"
                    params={{ slug: s.project, id: s.task_id }}
                    className="font-mono text-gray-600 hover:underline"
                  >
                    {s.task_id}
                  </Link>
                ) : (
                  <code className="text-gray-600">{s.task_id}</code>
                )}
                {s.action && (
                  <span className="rounded bg-gray-100 px-1 text-xs">
                    {s.action.replace('_', ' ')}
                  </span>
                )}
                <span>{s.text}</span>
                <span className="ml-auto flex gap-1">
                  <button
                    type="button"
                    className="rounded border px-1 text-xs disabled:text-gray-300"
                    disabled={!canDo(s)}
                    title={canDo(s) ? undefined : 'no known action on a known task'}
                    onClick={() => onDo(s)}
                  >
                    do
                  </button>
                  <button
                    type="button"
                    className="rounded border px-1 text-xs"
                    onClick={() => onDismiss(s)}
                  >
                    dismiss
                  </button>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}
