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

export interface ApproveView {
  /** The approve button is offered (a done review not approved yet). */
  offer: boolean
  label: string
  /** The confirmation question. */
  question: string
  /** Set when the report did not say "No issues found". */
  warning: string
  /** Lines saying what happened: approved, the Slack ✅, failures. */
  status: string[]
  /** An approved review whose Slack ✅ failed: the button retries it. */
  retryReaction: boolean
}

export function approveView(r: Review, now: Date = new Date()): ApproveView {
  const onSlack = r.slack ? ' and react ✅ on the Slack message' : ''
  const status: string[] = []
  if (r.approved) status.push(`approved on GitHub ${relativeTime(r.approved, now)}`)
  if (r.approve_error && !r.approved) status.push(`approve failed: ${r.approve_error}`)
  if (r.slack_reacted) status.push('✅ added on the Slack message')
  if (r.slack_react_error && !r.slack_reacted)
    status.push(`Slack ✅ failed: ${r.slack_react_error}`)
  return {
    offer: r.state === 'done' && !r.approved,
    label: r.no_issues ? 'approve on GitHub' : 'approve anyway',
    question: `Approve ${r.repo}#${r.number} on GitHub${onSlack}? Only an approve, no comment.`,
    warning: r.no_issues
      ? ''
      : 'The review did not say "No issues found" - read it before approving.',
    status,
    retryReaction: !!r.approved && !!r.slack && !r.slack_reacted,
  }
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
