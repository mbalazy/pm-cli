import { Link } from '@tanstack/react-router'
import { Sparkles } from 'lucide-react'

import type { ReportSuggestion } from '../api/types'
import { canDo, type ReportView } from '../lib/reportView'

// The LLM report over the feed (pm-cli-118-20): the period's prose, the
// cost in small print, and the open suggestions - each with "do" (the
// mapped action, through the page's dialog) and "dismiss". The state and
// the wording are lib/reportView's; the page owns the writing and the
// dismissing. Drawn as the paper's editorial column: a rule on the left,
// serif head, measured prose.

interface Props {
  view: ReportView
  onWrite: () => void
  onDo: (s: ReportSuggestion) => void
  onDismiss: (s: ReportSuggestion) => void
}

export function ReportPanel({ view, onWrite, onDo, onDismiss }: Props) {
  return (
    <section
      aria-label="Report"
      data-state={view.state}
      className="border-l-2 border-ink pl-4 text-sm"
    >
      <h2 className="mb-0.5 flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <span className="display text-[1.05rem]">Report</span>
        <span className="text-xs text-ink-3 italic">
          written by an LLM from the raw feed below, one per period · optional, costs tokens
        </span>
        {view.canWrite && (
          <button type="button" className="ghost-btn ml-auto" onClick={onWrite}>
            <Sparkles aria-hidden="true" className="size-3" strokeWidth={1.75} />
            {view.state === 'done' || view.state === 'error' ? 'write again' : 'write now'}
          </button>
        )}
      </h2>
      <p className="text-xs text-ink-2">
        {view.status}
        {view.cost && <span className="num ml-2 text-ink-3">· {view.cost}</span>}
      </p>
      {view.error && (
        <p role="alert" className="mt-1 text-crit">
          {view.error}
        </p>
      )}
      {view.paragraphs.length > 0 && (
        <div className="mt-3 max-w-[68ch] space-y-2 text-[0.9375rem] leading-relaxed">
          {view.paragraphs.map((p, i) => (
            <p key={i} className="whitespace-pre-wrap">
              {p}
            </p>
          ))}
        </div>
      )}
      {(view.open.length > 0 || view.dismissedCount > 0) && (
        <div className="mt-3">
          <h3 className="kicker mb-1">
            Suggestions
            {view.dismissedCount > 0 && (
              <span className="ml-1 normal-case text-ink-3">({view.dismissedCount} dismissed)</span>
            )}
          </h3>
          <ul>
            {view.open.map((s) => (
              <li
                key={s.id}
                className="ledger-row flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-2 py-1"
              >
                {s.project ? (
                  <Link
                    to="/p/$slug/t/$id"
                    params={{ slug: s.project, id: s.task_id }}
                    className="id hover:underline"
                  >
                    {s.task_id}
                  </Link>
                ) : (
                  <code className="id">{s.task_id}</code>
                )}
                {s.action && <span className="chip">{s.action.replace('_', ' ')}</span>}
                <span>{s.text}</span>
                <span className="row-actions ml-auto flex gap-1">
                  <button
                    type="button"
                    className="ghost-btn"
                    disabled={!canDo(s)}
                    title={canDo(s) ? undefined : 'no known action on a known task'}
                    onClick={() => onDo(s)}
                  >
                    do
                  </button>
                  <button type="button" className="ghost-btn" onClick={() => onDismiss(s)}>
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
