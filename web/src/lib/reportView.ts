import type { Report, ReportState, ReportSuggestion } from '../api/types'

// The report panel's rules: what each state says, which suggestions are
// still open, how the cost reads, how the prose splits into paragraphs.
// The state itself is the API's (/api/report); nothing here decides it.

export interface ReportView {
  state: ReportState['state']
  /** The one-line status under the heading. */
  status: string
  /** Prose paragraphs (markdown-ish text split on blank lines), empty unless done. */
  paragraphs: string[]
  /** Suggestions not yet dismissed. */
  open: ReportSuggestion[]
  dismissedCount: number
  /** "haiku · 4.1k tokens · 12 s" - the cost line, empty unless a report exists. */
  cost: string
  /** True when "write now" makes sense (enabled and not writing). */
  canWrite: boolean
  error?: string
}

export function reportView(r: ReportState | undefined): ReportView {
  if (!r) {
    return {
      state: 'none',
      status: 'loading…',
      paragraphs: [],
      open: [],
      dismissedCount: 0,
      cost: '',
      canWrite: false,
    }
  }
  const rep = r.report
  const open = openSuggestions(rep)
  const base = {
    state: r.state,
    paragraphs: [] as string[],
    open,
    dismissedCount: rep?.dismissed?.length ?? 0,
    cost: costLine(rep),
    canWrite: r.enabled && r.state !== 'writing',
  }
  switch (r.state) {
    case 'off':
      return {
        ...base,
        status: 'report is off (Settings › Sources › report) - it costs tokens',
        open: [],
      }
    case 'none':
      return {
        ...base,
        status: 'no report for this period yet - one is written after the first refresh, or now',
      }
    case 'writing':
      return { ...base, status: `writing… (${r.model}, up to 3 minutes)` }
    case 'error':
      return { ...base, status: 'the last attempt failed', error: rep?.error }
    case 'done':
      return {
        ...base,
        status: rep
          ? `written ${rep.generated} from ${rep.events} events and ${rep.rows} queue rows`
          : 'done',
        paragraphs: paragraphsOf(rep?.text ?? ''),
      }
    default:
      return { ...base, status: r.state }
  }
}

export function openSuggestions(rep: Report | undefined): ReportSuggestion[] {
  if (!rep) return []
  const dismissed = new Set(rep.dismissed ?? [])
  return rep.suggestions.filter((s) => !dismissed.has(s.id))
}

/** Total tokens the report cost (every token the model read plus what it wrote). */
export function totalTokens(t: Report['tokens']): number {
  if (!t) return 0
  return t.input + t.cache_creation + t.cache_read + t.output
}

export function costLine(rep: Report | undefined): string {
  if (!rep) return ''
  const parts = [rep.model]
  const n = totalTokens(rep.tokens)
  if (n > 0) parts.push(`${n >= 1000 ? `${(n / 1000).toFixed(1)}k` : n} tokens`)
  if (rep.duration_s > 0) parts.push(`${rep.duration_s} s`)
  return parts.join(' · ')
}

export function paragraphsOf(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p !== '')
}

/** A suggestion can be acted on when it names a known action on a placed task. */
export function canDo(s: ReportSuggestion): boolean {
  return s.action !== undefined && s.action !== '' && s.project !== undefined && s.project !== ''
}
