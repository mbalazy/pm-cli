import { Link } from '@tanstack/react-router'
import { GitPullRequest } from 'lucide-react'
import { useState } from 'react'

import { useRowMutation } from '../api/mutations'
import { useReviews } from '../api/queries'
import { parsePRUrl, reviewRow } from '../lib/reviewView'

// The Review screen: paste a GitHub PR URL, pm finds the project checking
// that repository out and runs `/review <url>` there headless; the list
// shows every review with its state, the report opens on its own page.

export function ReviewPage() {
  const reviews = useReviews()
  const start = useRowMutation()
  const [url, setUrl] = useState('')
  const pr = parsePRUrl(url)
  const now = new Date()
  const rows = (reviews.data?.reviews ?? []).map((r) => reviewRow(r, now))

  return (
    <div className="space-y-6">
      <header className="space-y-3 border-b-2 border-ink pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">Review</h1>
          <span className="text-sm text-ink-2">
            /review on a GitHub PR, run in the project's checkout · reported here, never posted
          </span>
        </div>
        <form
          aria-label="Start a review"
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (!pr) return
            start.mutate({ kind: 'review_start', url }, { onSuccess: () => setUrl('') })
          }}
        >
          <input
            type="url"
            aria-label="PR URL"
            placeholder="https://github.com/owner/repo/pull/123"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="field min-w-0 flex-1 basis-80"
          />
          <button type="submit" className="ghost-btn" disabled={!pr || start.isPending}>
            <GitPullRequest aria-hidden="true" className="size-3" strokeWidth={1.75} />
            {start.isPending ? 'starting…' : pr ? `review ${pr.repo}#${pr.number}` : 'review'}
          </button>
        </form>
        {start.isError && (
          <p role="alert" className="text-sm text-crit">
            {start.error.message}
          </p>
        )}
      </header>

      {reviews.isPending && <p className="text-ink-3">loading…</p>}
      {reviews.isError && <p className="text-crit">error: {reviews.error.message}</p>}
      {reviews.data && rows.length === 0 && (
        <p className="px-2 text-sm text-ink-3">No reviews yet - paste a PR URL above.</p>
      )}
      {rows.length > 0 && (
        <div className="overflow-x-auto">
          <table aria-label="Reviews" className="ledger-table">
            <thead>
              <tr>
                <th className="w-6" aria-label="state" />
                <th>PR</th>
                <th>project</th>
                <th>started</th>
                <th>took</th>
                <th>state</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.review.id}>
                  <td className="text-center">{r.glyph}</td>
                  <td>
                    <Link
                      to="/review/$id"
                      params={{ id: r.review.id }}
                      className="font-mono text-xs hover:underline"
                    >
                      {r.label}
                    </Link>
                  </td>
                  <td className="font-mono text-xs">{r.review.project}</td>
                  <td className="whitespace-nowrap text-ink-2">{r.when}</td>
                  <td className="num">{r.duration}</td>
                  <td className="text-sm text-ink-2">
                    {r.review.state === 'error' ? r.review.error : r.review.state}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
