import { Link } from '@tanstack/react-router'
import { EyeOff } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import { useRowMutation } from '../api/mutations'
import { useSoloReport } from '../api/queries'
import { relativeTime } from '../lib/relativeTime'

// One solo shift's report (pm-cli-136): the markdown the skill wrote, or the
// shift's state file while there is no report. "mark read" is the home
// row's dismiss.

export function SoloReportPage({ project, shift }: { project: string; shift: string }) {
  const report = useSoloReport(project, shift)
  const mark = useRowMutation()

  if (report.isPending) return <p className="text-ink-3">loading…</p>
  if (report.isError) return <p className="text-crit">error: {report.error.message}</p>
  const { shift: sh, kind, markdown } = report.data

  return (
    <div className="space-y-5">
      <header className="space-y-2 border-b-2 border-ink pb-3">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1 className="masthead">Solo report</h1>
          <span className="display text-lg text-ink-2">
            {sh.project} · {sh.date}
          </span>
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
        <p className="text-xs text-ink-2">
          {sh.open ? 'shift still open' : `closed ${sh.closed ? relativeTime(sh.closed) : ''}`}
          {' · '}
          {sh.tasks.map((t) => `${t.id} ${t.status ?? ''}`.trim()).join(', ') || 'no queue'}
          {kind === 'state' && ' · no report yet, showing the shift file'}
          {' · '}
          <Link to="/runs" className="underline">
            all shifts
          </Link>
        </p>
        {mark.isError && <p className="text-sm text-crit">{mark.error.message}</p>}
      </header>
      <article className="markdown max-w-[88ch] space-y-3 text-[0.9375rem] leading-relaxed [&_code]:font-mono [&_code]:text-[0.85em] [&_h1]:display [&_h1]:text-xl [&_h2]:display [&_h2]:mt-5 [&_h2]:text-lg [&_h3]:font-semibold [&_li]:ml-5 [&_ol]:list-decimal [&_pre]:overflow-x-auto [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:border-rule [&_td]:px-2 [&_th]:border [&_th]:border-rule [&_th]:px-2 [&_ul]:list-disc">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{markdown}</ReactMarkdown>
      </article>
    </div>
  )
}
