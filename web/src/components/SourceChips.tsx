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

export function SourceChips({ chips, counts, selected, onToggle }: Props) {
  return (
    <div role="group" aria-label="Sources" className="flex flex-wrap items-center gap-1 text-sm">
      <span className="text-xs text-gray-500">sources:</span>
      {chips.map((c) => {
        const on = selected.size === 0 || selected.has(c.name)
        return (
          <button
            key={c.name}
            type="button"
            aria-pressed={on}
            data-state={c.state}
            title={c.text}
            onClick={() => onToggle(c.name)}
            className={`rounded border px-2 py-0.5 text-xs ${on ? 'font-bold' : 'text-gray-400'} ${c.state === 'error' ? 'border-red-400 text-red-700' : ''}`}
          >
            {c.name} {counts.get(c.name) ?? 0}
            {GLYPH[c.state] && <span className="ml-1">{GLYPH[c.state]}</span>}
          </button>
        )
      })}
      {chips.some((c) => c.state === 'error') && (
        <ul className="w-full text-xs text-red-700">
          {chips
            .filter((c) => c.state === 'error')
            .map((c) => (
              <li key={c.name}>
                {c.name}: {c.text}
              </li>
            ))}
        </ul>
      )}
    </div>
  )
}
