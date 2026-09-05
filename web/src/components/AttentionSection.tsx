import type { AttentionRow as Row, AttentionSection as Section } from '../api/types'
import type { CappedSection } from '../lib/attentionView'
import { rowKey, sectionMeta } from '../lib/attentionView'
import { AttentionRow } from './AttentionRow'

// One home section: heading with the count and the "how it is counted" line,
// the capped rows, and the show-all / show-less toggle. The cap is applied
// upstream (lib/attentionView); this draws what it is handed.

interface Props {
  section: Section
  capped: CappedSection
  expanded: boolean
  onToggle: () => void
  selectedKey?: string
  onAction?: (action: string, row: Row) => void
}

export function AttentionSection({
  section,
  capped,
  expanded,
  onToggle,
  selectedKey,
  onAction,
}: Props) {
  const meta = sectionMeta(section.name)
  return (
    <section aria-label={meta.title} data-section={section.name}>
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2 border-b">
        <span className="font-semibold">{meta.title}</span>
        <span className="text-sm text-gray-500">
          {capped.rows.length < section.total
            ? `${capped.rows.length} of ${section.total}`
            : section.total}
        </span>
        {meta.why && <span className="text-xs text-gray-400">{meta.why}</span>}
      </h2>
      {section.note && <p className="text-xs text-gray-500">{section.note}</p>}
      {section.rows.length === 0 ? (
        <p className="text-sm text-gray-500">{meta.empty}</p>
      ) : (
        <ul className="space-y-0.5">
          {capped.rows.map((r) => (
            <AttentionRow
              key={rowKey(r)}
              row={r}
              selected={rowKey(r) === selectedKey}
              onAction={onAction}
            />
          ))}
        </ul>
      )}
      {(capped.collapsed > 0 || expanded) && section.rows.length > 0 && (
        <button type="button" className="mt-1 text-xs underline" onClick={onToggle}>
          {expanded ? 'show less' : `show all (${section.rows.length})`}
        </button>
      )}
      {capped.elsewhere > 0 && (
        <p className="mt-1 text-xs text-gray-400">
          +{capped.elsewhere} more on this section's screen
        </p>
      )}
    </section>
  )
}
