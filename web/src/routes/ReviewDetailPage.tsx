import { Link } from '@tanstack/react-router'
import { Check, Square } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import { useRowMutation } from '../api/mutations'
import { useReview } from '../api/queries'
import { approveView, reviewRow, slackSeenLine } from '../lib/reviewView'

// One PR code review: the state while it runs (with cancel), the error when
// it failed, the report's markdown when it is done - and the approve: ONE
// click approves on GitHub (and reacts ✅ on the Slack message the request
// came from). No confirmation step, the user's call: the button only exists
// on a finished review, and its label says "approve anyway" with a warning
// when the report found issues.

export function ReviewDetailPage({ id }: { id: string }) {
  const review = useReview(id)
  const cancel = useRowMutation()
  const approve = useRowMutation()

  if (review.isPending) return <p className="text-ink-3">loading…</p>
  if (review.isError) return <p className="text-crit">error: {review.error.message}</p>
  const r = review.data
  const row = reviewRow(r)
  const av = approveView(r)
  const runApprove = () => approve.mutate({ kind: 'review_approve', id: r.id })

  return (
    <div className="space-y-5">
      <header className="space-y-2 border-b-2 border-ink pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">Review</h1>
          <a href={r.url} target="_blank" rel="noreferrer" className="display text-lg underline">
            {row.label}
          </a>
          {r.state === 'running' && (
            <button
              type="button"
              className="ghost-btn ml-auto"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate({ kind: 'review_cancel', id: r.id })}
            >
              <Square aria-hidden="true" className="size-3" strokeWidth={1.75} />
              cancel
            </button>
          )}
        </div>
        <p className="text-xs text-ink-2">
          {row.glyph} {r.state} · {row.duration} · project {r.project} ·{' '}
          <span className="font-mono">{r.dir}</span> · account{' '}
          <span className="font-mono">{r.config_dir}</span> ·{' '}
          <Link to="/review" className="underline">
            all reviews
          </Link>
        </p>
        {r.title && <p className="display text-base">{r.title}</p>}
        {r.input && <p className="text-xs text-ink-3">asked as: {r.input}</p>}
        {slackSeenLine(r) && <p className="text-xs text-ink-2">{slackSeenLine(r)}</p>}
        {r.commit && (
          <p className="text-xs text-ink-3">
            ran in a throwaway worktree at <span className="font-mono">{r.commit}</span> - your
            checkout is never touched
          </p>
        )}
      </header>

      {r.state === 'done' && (
        <section aria-label="Approve" className="space-y-2">
          {av.status.map((line) => (
            <p key={line} className="text-sm">
              {line}
            </p>
          ))}
          {av.offer && (
            <button
              type="button"
              className="ghost-btn"
              title={av.question}
              disabled={approve.isPending}
              onClick={runApprove}
            >
              <Check aria-hidden="true" className="size-3" strokeWidth={1.75} />
              {approve.isPending ? 'approving…' : av.label}
            </button>
          )}
          {av.offer && av.warning && <p className="text-xs text-warn">{av.warning}</p>}
          {av.retryReaction && (
            <button
              type="button"
              className="ghost-btn"
              disabled={approve.isPending}
              onClick={runApprove}
            >
              retry ✅ on Slack
            </button>
          )}
          {approve.isError && (
            <p role="alert" className="text-sm text-crit">
              {approve.error.message}
            </p>
          )}
        </section>
      )}

      {r.state === 'running' && (
        <p className="text-sm text-ink-2">
          Running /review in the checkout - a few minutes for a normal PR. This page refreshes by
          itself.
        </p>
      )}
      {r.state === 'error' && (
        <p role="alert" className="text-sm text-crit">
          {r.error}
        </p>
      )}
      {r.report && (
        <article className="markdown max-w-[88ch] space-y-3 text-[0.9375rem] leading-relaxed [&_code]:font-mono [&_code]:text-[0.85em] [&_h1]:display [&_h1]:text-xl [&_h2]:display [&_h2]:mt-5 [&_h2]:text-lg [&_h3]:display [&_h3]:text-lg [&_li]:ml-5 [&_ol]:list-decimal [&_pre]:overflow-x-auto [&_ul]:list-disc">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{r.report}</ReactMarkdown>
        </article>
      )}
    </div>
  )
}
