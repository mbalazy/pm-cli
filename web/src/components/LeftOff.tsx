import { PenLine } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { Project } from '../api/types'
import type { LeftOffView } from '../lib/timelineView'
import { SectionHead } from './SectionHead'

// "Where we left off": one block per member repo. A repo with a timeline
// shows its latest state, the entries after it and the stale line; a repo
// without one shows its hand-written notes with the edit action, which ASKS -
// the page opens the confirmation dialog. What each repo shows is decided in
// lib/timelineView.

export interface LeftOffRow {
  project: Project
  view: LeftOffView
}

interface Props {
  rows: LeftOffRow[]
  onEdit?: (project: Project) => void
}

export function LeftOff({ rows, onEdit }: Props) {
  return (
    <section aria-label="Where we left off">
      <SectionHead
        title="Where we left off"
        why="each repo's latest timeline state and what happened since · notes where a repo keeps no timeline"
      />
      <ul className="space-y-4">
        {rows.map(({ project: m, view }) => (
          <li key={m.slug} aria-label={m.slug} className="text-sm">
            <div className="mb-1 flex items-center gap-2">
              {rows.length > 1 && <span className="chip">{m.slug}</span>}
              {view.kind === 'notes' && onEdit && (
                <button type="button" className="ghost-btn" onClick={() => onEdit(m)}>
                  <PenLine aria-hidden="true" className="size-3" strokeWidth={1.75} />
                  {view.notes ? 'edit notes' : 'add notes'}
                </button>
              )}
            </div>
            {view.kind === 'timeline' ? (
              <div className="space-y-2">
                {view.state && (
                  <div className="border-l-2 border-rule-strong pl-3">
                    <p className="text-xs text-ink-3">
                      state · <span className="num">{view.state.date}</span>
                    </p>
                    <div className="markdown">
                      <Markdown remarkPlugins={[remarkGfm]}>{view.state.text}</Markdown>
                    </div>
                  </div>
                )}
                <p className="text-xs text-ink-3 italic">{view.heading}</p>
                {view.entries.length > 0 && (
                  <ol aria-label={`${m.slug} timeline entries`} className="space-y-1">
                    {view.entries.map((e) => (
                      <li key={e.id} className="flex flex-wrap items-baseline gap-x-2">
                        <span className="num text-xs text-ink-3">{e.date}</span>
                        <span className="text-xs text-ink-2">{e.kind}</span>
                        <span className="whitespace-pre-line">{e.text}</span>
                        {e.refs.map((r) =>
                          r.href ? (
                            <a
                              key={r.label}
                              href={r.href}
                              target="_blank"
                              rel="noreferrer"
                              className="text-xs underline"
                            >
                              {r.label}
                            </a>
                          ) : (
                            <span key={r.label} className="id text-xs text-ink-3">
                              {r.label}
                            </span>
                          ),
                        )}
                      </li>
                    ))}
                  </ol>
                )}
                {view.stale && <p className="text-xs text-warn">{view.stale}</p>}
              </div>
            ) : view.notes ? (
              <div className="markdown border-l-2 border-rule-strong pl-3">
                <Markdown remarkPlugins={[remarkGfm]}>{view.notes}</Markdown>
              </div>
            ) : (
              <p className="text-ink-3 italic">no notes yet</p>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
