import { Link } from '@tanstack/react-router'
import { EyeOff } from 'lucide-react'

import { useRowMutation } from '../api/mutations'
import { useSoloReport } from '../api/queries'
import { Markdown, OutcomeChips, SoloDigest } from '../components/SoloDigest'
import { relativeTime } from '../lib/relativeTime'
import { digestOf, digestView, outcomeChips, reportTitle } from '../lib/soloReportView'

// One solo shift's report (pm-cli-136): the digest of what came out and
// what the user is asked to do, the long parts folded; a report the server
// could not split (or a shift with no report yet) renders whole. "mark read"
// is the home row's dismiss.

export function SoloReportPage({ project, shift }: { project: string; shift: string }) {
  const report = useSoloReport(project, shift)
  const mark = useRowMutation()

  if (report.isPending) return <p className="text-ink-3">loading…</p>
  if (report.isError) return <p className="text-crit">error: {report.error.message}</p>
  const { shift: sh, kind, markdown } = report.data
  const digest = digestOf(report.data)

  return (
    <div className="max-w-[88ch] space-y-5">
      <header className="space-y-2 border-b-2 border-ink pb-3">
        <p className="text-xs text-ink-2">
          solo shift · {sh.project} · {sh.date} ·{' '}
          {sh.open ? 'still open' : `closed ${sh.closed ? relativeTime(sh.closed) : ''}`}
          {kind === 'state' && ' · no report yet, showing the shift file'}
          {' · '}
          <Link to="/runs" className="underline">
            all shifts
          </Link>
        </p>
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">{reportTitle(report.data)}</h1>
          {!sh.open && sh.report && (
            <button
              type="button"
              className="ghost-btn ml-auto"
              disabled={mark.isPending || mark.isSuccess}
              onClick={() =>
                mark.mutate({
                  kind: 'dismiss',
                  rows: [
                    {
                      section: 'solo_reports',
                      project: sh.project,
                      shift: sh.id,
                      since: sh.closed,
                    },
                  ],
                })
              }
            >
              <EyeOff aria-hidden="true" className="size-3" strokeWidth={1.75} />
              {mark.isSuccess ? 'marked read' : 'mark read'}
            </button>
          )}
        </div>
        <OutcomeChips chips={outcomeChips(sh.summary)} />
        {mark.isError && <p className="text-sm text-crit">{mark.error.message}</p>}
      </header>
      {digest ? (
        <SoloDigest view={digestView(digest, markdown)} />
      ) : (
        <article aria-label="Report">
          <Markdown md={markdown} className="text-[0.9375rem]" />
        </article>
      )}
    </div>
  )
}
