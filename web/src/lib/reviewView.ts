import type { Review } from '../api/types'
import { relativeTime } from './relativeTime'

// The Review screen's rules: which input is a PR URL (the server decides
// for real - this only enables the button), and how a review is worded in
// the list.

const PR_URL = /^https?:\/\/(?:www\.)?github\.com\/([\w.-]+)\/([\w.-]+)\/pull\/(\d+)(?:[/?#].*)?$/

export function parsePRUrl(text: string): { repo: string; number: number } | null {
  const m = PR_URL.exec(text.trim())
  if (!m) return null
  return { repo: `${m[1]}/${m[2]}`, number: Number(m[3]) }
}

export interface ReviewRow {
  review: Review
  /** ▶ running, ✓ done, ✗ error. */
  glyph: string
  /** owner/repo#n */
  label: string
  /** When it started, relative. */
  when: string
  /** How long it ran (to now while running). */
  duration: string
}

export function durationText(ms: number): string {
  const min = Math.max(0, Math.round(ms / 60000))
  if (min < 60) return `${min}m`
  return `${Math.floor(min / 60)}h ${min % 60}m`
}

export function reviewRow(r: Review, now: Date = new Date()): ReviewRow {
  const start = Date.parse(r.started)
  const end = r.state === 'running' || !r.finished ? now.getTime() : Date.parse(r.finished)
  return {
    review: r,
    glyph: r.state === 'running' ? '▶' : r.state === 'done' ? '✓' : '✗',
    label: `${r.repo}#${r.number}`,
    when: relativeTime(r.started, now),
    duration: Number.isNaN(start) ? '' : durationText(end - start),
  }
}
