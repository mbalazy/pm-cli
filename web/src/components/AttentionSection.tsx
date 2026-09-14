import type { AttentionRow as Row, AttentionSection as Section } from '../api/types'
import type { CappedSection } from '../lib/attentionView'
import { DISMISS_OLDER_DAYS, olderThan, rowKey, sectionMeta } from '../lib/attentionView'
import { AttentionRow } from './AttentionRow'
import { SectionHead } from './SectionHead'

// One home section: heading with the count and the "how it is counted" line,
// the capped rows, and the show-all / show-less toggle. The cap is applied
// upstream (lib/attentionView); this draws what it is handed. With the
// dismiss handlers it also offers "dismiss older than 14d" and
// "N hidden · restore".

interface Props {
  section: Section
  capped: CappedSection
  expanded: boolean
  onToggle: () => void
  selectedKey?: string
  onAction?: (action: string, row: Row) => void
  /** Dismiss these rows at once (the bulk "older than" button). */
  onDismissRows?: (rows: Row[]) => void
  /** Bring the section's dismissed rows back. */
  onRestore?: () => void
  /** The entrance stagger index (the home page's order). */
  index?: number
}

export function AttentionSection({
  section,
  capped,
  expanded,
  onToggle,
  selectedKey,
  onAction,
  onDismissRows,
  onRestore,
  index,
}: Props) {
  const meta = sectionMeta(section.name)
  const count =
    capped.rows.length < section.total ? `${capped.rows.length} of ${section.total}` : section.total
  const older = onDismissRows ? olderThan(section.rows, DISMISS_OLDER_DAYS) : []
  const hidden = section.dismissed ?? 0
  return (
    <section
      aria-label={meta.title}
      data-section={section.name}
      className={index === undefined ? undefined : 'rise'}
      style={index === undefined ? undefined : ({ '--i': index } as React.CSSProperties)}
    >
      <SectionHead title={meta.title} count={count} why={meta.why} />
      {section.note && <p className="mb-1 text-xs text-ink-3 italic">{section.note}</p>}
      {(older.length > 0 || (onRestore && hidden > 0)) && (
        <p className="mb-1 flex flex-wrap items-center gap-2 px-2 text-xs text-ink-2">
          {older.length > 0 && (
            <button type="button" className="ghost-btn" onClick={() => onDismissRows?.(older)}>
              dismiss older than {DISMISS_OLDER_DAYS}d ({older.length})
            </button>
          )}
          {onRestore && hidden > 0 && (
            <button type="button" className="ghost-btn" onClick={onRestore}>
              {hidden} hidden · restore
            </button>
          )}
        </p>
      )}
      {section.rows.length === 0 ? (
        <p className="px-2 py-1 text-sm text-ink-3">{meta.empty}</p>
      ) : (
        <ul>
          {capped.rows.map((r, i) => (
            // The changes digest can list one task several times (one row
            // per event), so the key carries the position too.
            <AttentionRow
              key={`${rowKey(r)}#${i}`}
              row={r}
              selected={rowKey(r) === selectedKey}
              onAction={onAction}
            />
          ))}
        </ul>
      )}
      {(capped.collapsed > 0 || expanded) && section.rows.length > 0 && (
        <button
          type="button"
          className="mt-1 px-2 text-xs text-ink-2 underline decoration-rule-strong underline-offset-2 hover:text-ink"
          onClick={onToggle}
        >
          {expanded ? 'show less' : `show all (${section.rows.length})`}
        </button>
      )}
      {capped.elsewhere > 0 && (
        <p className="mt-1 px-2 text-xs text-ink-3 italic">
          +{capped.elsewhere} more on this section's screen
        </p>
      )}
    </section>
  )
}
