import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { DigestView, OutcomeChip } from '../lib/soloReportView'
import { Glyph } from './Glyph'
import { SectionHead } from './SectionHead'
import { cn } from './ui/cn'

// A solo report as the parts a reader acts on: the move asked of the user,
// the outcome of each task in one line (opened on demand), and the long
// sections folded. Everything comes worded by lib/soloReportView.

const MD =
  'space-y-2 leading-relaxed [&_code]:font-mono [&_code]:text-[0.85em] [&_h1]:display [&_h1]:text-lg [&_h2]:display [&_h2]:mt-4 [&_h2]:text-base [&_h3]:font-semibold [&_li]:ml-5 [&_ol]:list-decimal [&_pre]:overflow-x-auto [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:border-rule [&_td]:px-2 [&_th]:border [&_th]:border-rule [&_th]:px-2 [&_ul]:list-disc'

export function Markdown({ md, className }: { md: string; className?: string }) {
  return (
    <div className={cn(MD, className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{md}</ReactMarkdown>
    </div>
  )
}

export function OutcomeChips({ chips }: { chips: OutcomeChip[] }) {
  if (chips.length === 0) return null
  return (
    <ul aria-label="Outcomes" className="flex flex-wrap gap-1.5">
      {chips.map((c) => (
        <li key={c.label} className={cn('chip', c.tone && `chip-${c.tone}`)}>
          {c.label}
        </li>
      ))}
    </ul>
  )
}

export function SoloDigest({ view }: { view: DigestView }) {
  return (
    <div className="space-y-6">
      {(view.next || view.summary) && (
        <section aria-label="Your move" className="space-y-1.5">
          {view.next && (
            <div className="border-l-4 border-warn bg-warn-bg px-4 py-3">
              <p className="kicker mb-1">your move</p>
              <Markdown md={view.next} className="text-base text-ink" />
            </div>
          )}
          {view.summary && <Markdown md={view.summary} className="text-sm text-ink-2" />}
        </section>
      )}

      <section aria-label="Tasks">
        <SectionHead title="Tasks" count={view.tasks.length} why="click a task for the details" />
        <ol aria-label="Tasks">
          {view.tasks.map((t) => (
            <li key={t.key} className="ledger-row">
              <details className="group">
                <summary className="flex cursor-pointer list-none items-start gap-2 px-1 py-2 [&::-webkit-details-marker]:hidden">
                  <Glyph
                    glyph={t.outcome.glyph}
                    severity={t.outcome.tone}
                    label={`outcome: ${t.outcome.label}`}
                    className="mt-0.5"
                  />
                  <span className="min-w-0 flex-1 space-y-0.5">
                    <span className="flex flex-wrap items-baseline gap-x-2">
                      <span className="font-medium">{t.heading}</span>
                      <span className={cn('chip', t.outcome.tone && `chip-${t.outcome.tone}`)}>
                        {t.outcome.label}
                      </span>
                    </span>
                    {t.lead && (
                      <span className="block text-sm text-ink-2 group-open:hidden">{t.lead}</span>
                    )}
                    {t.beforePR && (
                      <span className="block text-sm group-open:hidden">
                        <span className="kicker mr-1.5">before the PR</span>
                        {t.beforePR}
                      </span>
                    )}
                  </span>
                  <span aria-hidden="true" className="text-ink-3 group-open:rotate-90">
                    ›
                  </span>
                </summary>
                <div className="space-y-3 pb-3 pl-7 text-[0.9375rem]">
                  {t.parts.map((p) => (
                    <div key={p.label}>
                      <p className="kicker">{p.label}</p>
                      <Markdown md={p.md} />
                    </div>
                  ))}
                </div>
              </details>
            </li>
          ))}
        </ol>
      </section>

      <div className="space-y-2">
        {view.folds.map((f) => (
          <details key={f.label} className="border-t border-rule pt-2">
            <summary className="display cursor-pointer text-[1.05rem]">{f.label}</summary>
            <div className="pt-2 text-[0.9375rem]">
              {f.items ? (
                <ol className="list-decimal space-y-2 pl-5">
                  {f.items.map((item, i) => (
                    <li key={i}>
                      <Markdown md={item} />
                    </li>
                  ))}
                </ol>
              ) : (
                <Markdown md={f.md ?? ''} />
              )}
            </div>
          </details>
        ))}
      </div>
    </div>
  )
}
