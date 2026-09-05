import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import type { Project } from '../api/types'

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
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
        <span className="font-semibold">Where we left off</span>
        <span className="text-xs text-gray-400">
          v1: project notes, written by hand · later: a brief written at the end of the day
        </span>
      </h2>
      <ul className="space-y-2">
        {members.map((m) => (
          <li key={m.slug} className="text-sm">
            <div className="flex items-baseline gap-2">
              {members.length > 1 && (
                <span className="rounded bg-gray-100 px-1 text-xs text-gray-600">{m.slug}</span>
              )}
              {onEdit && (
                <button
                  type="button"
                  className="rounded border px-1 text-xs"
                  onClick={() => onEdit(m)}
                >
                  {m.notes ? 'edit notes' : 'add notes'}
                </button>
              )}
            </div>
            {m.notes ? (
              <div className="markdown">
                <Markdown remarkPlugins={[remarkGfm]}>{m.notes}</Markdown>
              </div>
            ) : (
              <p className="text-gray-400">no notes yet</p>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
