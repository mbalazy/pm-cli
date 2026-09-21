import type { Review } from '../api/types'
import { relativeTime } from './relativeTime'

// The Review screen's rules: which input is a change request URL (the server
// decides for real - this only enables the button), and how a review is
// worded in the list.

// The hosts, mirrored from internal/review/host.go: how each writes a change
// request's URL and its number after the repository (o/r#7 on GitHub,
// group/sub/repo!43 on GitLab, whose groups nest). An absent host on a review
// is GitHub - every review stored before GitLab support was one.
const HOSTS = {
  github: {
    display: 'GitHub',
    sigil: '#',
    url: /^https?:\/\/(?:www\.)?github\.com\/([\w.-]+\/[\w.-]+)\/pull\/(\d+)(?:[/?#].*)?$/,
  },
  gitlab: {
    display: 'GitLab',
    sigil: '!',
    url: /^https?:\/\/(?:www\.)?gitlab\.com\/([\w.-]+(?:\/[\w.-]+)+?)\/-\/merge_requests\/(\d+)(?:[/?#].*)?$/,
  },
} as const

export type HostName = keyof typeof HOSTS

export function host(name: string | undefined): (typeof HOSTS)[HostName] {
  return HOSTS[(name ?? '') as HostName] ?? HOSTS.github
}

/** o/r#7, group/sub/repo!43. */
export function changeLabel(hostName: string | undefined, repo: string, number: number): string {
  return `${repo}${host(hostName).sigil}${number}`
}

export function parsePRUrl(
  text: string,
): { host: HostName; repo: string; number: number; label: string } | null {
  const trimmed = text.trim()
  for (const name of Object.keys(HOSTS) as HostName[]) {
    const m = HOSTS[name].url.exec(trimmed)
    if (m)
      return {
        host: name,
        repo: m[1],
        number: Number(m[2]),
        label: changeLabel(name, m[1], Number(m[2])),
      }
  }
  return null
}

export interface ReviewRow {
  review: Review
  /** ▶ running, ✓ done, ✗ error. */
  glyph: string
  /** o/r#7 on GitHub, group/sub/repo!43 on GitLab. */
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
  const where = host(r.host).display
  const status: string[] = []
  if (r.approved) status.push(`approved on ${where} ${relativeTime(r.approved, now)}`)
  if (r.approve_error && !r.approved) status.push(`approve failed: ${r.approve_error}`)
  if (r.slack_reacted) status.push('✅ added on the Slack message')
  if (r.slack_react_error && !r.slack_reacted)
    status.push(`Slack ✅ failed: ${r.slack_react_error}`)
  return {
    offer: r.state === 'done' && !r.approved,
    label: r.no_issues ? `approve on ${where}` : 'approve anyway',
    question: `Approve ${changeLabel(r.host, r.repo, r.number)} on ${where}${onSlack}? Only an approve, no comment.`,
    warning: r.no_issues
      ? ''
      : 'The review did not say "No issues found" - read it before approving.',
    status,
    retryReaction: !!r.approved && !!r.slack && !r.slack_reacted,
  }
}

/** The 👀 the start put on the Slack message (any state), '' for a non-Slack review. */
export function slackSeenLine(r: Review): string {
  if (r.slack_seen) return '👀 added on the Slack message'
  if (r.slack_seen_error) return `Slack 👀 failed: ${r.slack_seen_error}`
  if (r.slack && r.state === 'running') return '👀 going on the Slack message…'
  return ''
}

export function reviewRow(r: Review, now: Date = new Date()): ReviewRow {
  const start = Date.parse(r.started)
  const end = r.state === 'running' || !r.finished ? now.getTime() : Date.parse(r.finished)
  return {
    review: r,
    glyph: r.state === 'running' ? '▶' : r.state === 'done' ? '✓' : '✗',
    label: changeLabel(r.host, r.repo, r.number),
    when: relativeTime(r.started, now),
    duration: Number.isNaN(start) ? '' : durationText(end - start),
  }
}
