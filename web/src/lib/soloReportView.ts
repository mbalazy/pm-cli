import type { ShiftReport, ShiftSummary, SoloReportResult } from '../api/types'

// The solo report page and the solo table (pm-cli-136): how an outcome
// reads, the counts, and the short lines a task shows before it is opened.
// The report is split into parts by the server (storage.ParseShiftReport);
// nothing here parses sections, it only shortens what is already split.

export interface OutcomeView {
  glyph: string
  label: string
  /** A severity name for the ink ('' = quiet). */
  tone: string
}

const OUTCOMES: Record<string, OutcomeView> = {
  done: { glyph: '✓', label: 'done', tone: 'ok' },
  partial: { glyph: '◐', label: 'partial', tone: 'warn' },
  not_done: { glyph: '✗', label: 'not done', tone: 'crit' },
  parked: { glyph: '⏸', label: 'parked', tone: 'warn' },
  untouched: { glyph: '○', label: 'untouched', tone: '' },
  doing: { glyph: '▶', label: 'in progress', tone: 'info' },
}
const NO_VERDICT: OutcomeView = { glyph: '·', label: 'no verdict', tone: '' }

export function outcomeView(outcome?: string): OutcomeView {
  return OUTCOMES[outcome ?? ''] ?? NO_VERDICT
}

export interface OutcomeChip {
  label: string
  tone: string
}

/** One chip per non-zero count, in the order done › partial › not done › parked › untouched. */
export function outcomeChips(s?: ShiftSummary): OutcomeChip[] {
  if (!s) return []
  const counts: [number, string][] = [
    [s.done, 'done'],
    [s.partial, 'partial'],
    [s.not_done, 'not_done'],
    [s.parked, 'parked'],
    [s.untouched, 'untouched'],
  ]
  return counts
    .filter(([n]) => n > 0)
    .map(([n, key]) => ({ label: `${n} ${OUTCOMES[key].label}`, tone: OUTCOMES[key].tone }))
}

/** "6 done · 1 untouched" - the Go side's ShiftSummary.Counts. */
export function countsText(s?: ShiftSummary): string {
  return outcomeChips(s)
    .map((c) => c.label)
    .join(' · ')
}

/** A task came back partial, not done or parked. */
export function unfinished(s?: ShiftSummary): boolean {
  return !!s && s.partial + s.not_done + s.parked > 0
}

/** Markdown to one line of text: links to their text, no bold, no code ticks. */
export function plainText(md: string): string {
  return md
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\*\*|`/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

const LIST_ITEM = /^\s*(?:[-*]|\d+[.)])\s/

/** The text before the first blank line or list. */
function firstParagraph(md: string): string {
  const out: string[] = []
  for (const line of md.split('\n')) {
    if (line.trim() === '' || LIST_ITEM.test(line)) {
      if (out.length > 0) break
      continue
    }
    out.push(line.trim())
  }
  return out.join(' ')
}

// The verdict word "Stan:" opens with; the outcome chip already says it.
const VERDICT = /^(?:nie )?(?:zrobione|naprawione|gotowe|dowiezione)\b\s*[.:]?\s*/i

/**
 * The first paragraph as one line of at most `max` characters, the verdict
 * word dropped, cut at a sentence when one ends in the last two thirds,
 * else at a word.
 */
export function leadOf(md: string | undefined, max = 200): string {
  let text = plainText(firstParagraph(md ?? '')).replace(VERDICT, '')
  text = text.charAt(0).toUpperCase() + text.slice(1)
  if (text.length <= max) return text
  const cut = text.slice(0, max)
  const dot = cut.lastIndexOf('. ')
  if (dot >= max / 3) return cut.slice(0, dot + 1)
  const space = cut.lastIndexOf(' ')
  return `${cut.slice(0, space > max / 2 ? space : max).replace(/[\s,;:-]+$/, '')}…`
}

const NOTHING = new Set(['', '-', 'nic', 'brak', 'none', 'nothing', 'n/a'])

/** False for an empty field or one that only says "nic." / "Brak.". */
export function meaningful(md?: string): boolean {
  return !NOTHING.has(
    plainText(md ?? '')
      .replace(/[.!]+$/, '')
      .toLowerCase(),
  )
}

export function bulletCount(md?: string): number {
  return (md ?? '').split('\n').filter((l) => LIST_ITEM.test(l)).length
}

/** "scalić po kroku 1. Decyzje do potwierdzenia: (+4 points)", '' when there is nothing to do. */
export function beforePRLine(md?: string): string {
  if (!meaningful(md)) return ''
  const n = bulletCount(md)
  const lead = leadOf(md, 140)
  return n > 0 ? `${lead} (+${n} ${n === 1 ? 'point' : 'points'})` : lead
}

export interface TaskCard {
  key: string
  heading: string
  outcome: OutcomeView
  /** The state in one line - what a closed card shows. */
  lead: string
  /** What is left before the PR, one line, '' when nothing. */
  beforePR: string
  /** The full fields an opened card shows, markdown, empty ones left out. */
  parts: { label: string; md: string }[]
}

export interface Fold {
  label: string
  /** A list section (decisions, ideas), one markdown string per item. */
  items?: string[]
  /** A prose section, markdown. */
  md?: string
}

export interface DigestView {
  next: string
  summary: string
  tasks: TaskCard[]
  folds: Fold[]
}

/** The page's parts in reading order: the move, the tasks, then what is folded. */
export function digestView(d: ShiftReport, markdown: string): DigestView {
  const tasks = d.tasks.map((t, i): TaskCard => {
    const parts: { label: string; md: string }[] = [
      { label: 'State', md: t.state ?? '' },
      { label: 'Before the PR', md: meaningful(t.before_pr) ? (t.before_pr ?? '') : '' },
      { label: 'Problem', md: t.problem ?? '' },
      { label: 'How it was checked', md: t.checked ?? '' },
    ]
    return {
      key: `${i}-${t.heading}`,
      heading: t.heading,
      outcome: outcomeView(t.outcome),
      lead: leadOf(t.state),
      beforePR: beforePRLine(t.before_pr),
      parts: parts.filter((p) => p.md.trim() !== ''),
    }
  })
  const folds: Fold[] = []
  if (d.decisions.length > 0) {
    folds.push({ label: `Decisions made for you (${d.decisions.length})`, items: d.decisions })
  }
  if (d.ideas.length > 0) {
    folds.push({ label: `Ideas for new tickets (${d.ideas.length})`, items: d.ideas })
  }
  if (meaningful(d.cleanup)) folds.push({ label: 'Cleanup and runtime', md: d.cleanup })
  if (meaningful(d.technical)) folds.push({ label: 'Technical details', md: d.technical })
  folds.push({ label: 'Whole report', md: markdown })
  return { next: d.next ?? '', summary: d.summary ?? '', tasks, folds }
}

/** The digest when the report split into tasks; a state file or an unsplit report renders whole. */
export function digestOf(res: SoloReportResult): ShiftReport | undefined {
  return res.kind === 'report' && res.digest && res.digest.tasks.length > 0 ? res.digest : undefined
}

/** The shift's name: the summary's title, the report's own, else project · date. */
export function reportTitle(res: SoloReportResult): string {
  return res.shift.summary?.title || res.digest?.title || `${res.shift.project} · ${res.shift.date}`
}
