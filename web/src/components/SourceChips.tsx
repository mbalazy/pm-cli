import type { SourceChip } from '../lib/changesView'

// The source filter and status in one strip: a chip per source with its
// event count, pressed = in the filter, and its state (last fetch, error,
// off) as the title and a small glyph. Which sources, their counts and
// their states are the API's; the pressed set is the page's.

interface Props {
  chips: SourceChip[]
  counts: Map<string, number>
  /** Sources in the filter; empty = all. */
  selected: ReadonlySet<string>
  onToggle: (source: string) => void
}

const GLYPH: Record<SourceChip['state'], string> = { ok: '', never: '·', error: '✗', off: '–' }

/** The collapsed line: "N projects errored" when the text is a joined
 *  per-project list (the feed joins them as "slug: msg slug: msg"), else just
 *  "error" - the text itself is shown once, under the summary. */
function errorSummary(text: string): string {
  const projects = text.match(/(^|\s)[a-z0-9._-]+: /g)
  if (projects && projects.length > 1) return `${projects.length} projects errored`
  return 'error'
}

export function SourceChips({ chips, counts, selected, onToggle }: Props) {
  return (
    <div role="group" aria-label="Sources" className="flex flex-wrap items-center gap-1 text-sm">
      <span className="kicker mr-1">sources</span>
      {chips.map((c) => {
        const on = selected.size === 0 || selected.has(c.name)
        return (
          <button
            key={c.name}
            type="button"
            aria-pressed={on}
            data-state={c.state}
            data-tone={c.state === 'error' ? 'crit' : undefined}
            title={c.text}
            onClick={() => onToggle(c.name)}
            className="pill"
          >
            {c.name} <span className="num text-[0.7rem] opacity-80">{counts.get(c.name) ?? 0}</span>
            {GLYPH[c.state] && <span className="ml-0.5">{GLYPH[c.state]}</span>}
          </button>
        )
      })}
      {chips.some((c) => c.state === 'error') && (
        <details className="w-full text-xs">
          <summary className="cursor-pointer text-crit">
            {chips
              .filter((c) => c.state === 'error')
              .map((c) => `${c.name}: ${errorSummary(c.text)}`)
              .join(' · ')}
            <span className="ml-1 text-ink-3">(show)</span>
          </summary>
          <ul className="mt-1 space-y-1 border-l-2 border-crit pl-2 text-crit">
            {chips
              .filter((c) => c.state === 'error')
              .map((c) => (
                <li key={c.name}>
                  {c.name}: {c.text}
                </li>
              ))}
          </ul>
        </details>
      )}
    </div>
  )
}
