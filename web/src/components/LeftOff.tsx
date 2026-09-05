import { PenLine } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { Project } from '../api/types'
import { SectionHead } from './SectionHead'

// "Where we left off": the notes of every member repo (v1 = hand-written; a
// generated evening brief is a later epic). One block per repo that has
// notes, an "add" button for one that has none; the edit ASKS - the page
// opens the confirmation dialog.

interface Props {
  members: Project[]
  onEdit?: (project: Project) => void
}

export function LeftOff({ members, onEdit }: Props) {
  return (
    <section aria-label="Where we left off">
      <SectionHead
        title="Where we left off"
        why="v1: project notes, written by hand · later: a brief written at the end of the day"
      />
      <ul className="space-y-4">
        {members.map((m) => (
          <li key={m.slug} className="text-sm">
            <div className="mb-1 flex items-center gap-2">
              {members.length > 1 && <span className="chip">{m.slug}</span>}
              {onEdit && (
                <button type="button" className="ghost-btn" onClick={() => onEdit(m)}>
                  <PenLine aria-hidden="true" className="size-3" strokeWidth={1.75} />
                  {m.notes ? 'edit notes' : 'add notes'}
                </button>
              )}
            </div>
            {m.notes ? (
              <div className="markdown border-l-2 border-rule-strong pl-3">
                <Markdown remarkPlugins={[remarkGfm]}>{m.notes}</Markdown>
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
