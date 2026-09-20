import { describe, expect, it } from 'vitest'

import type { Review } from '../api/types'
import { approveView, durationText, parsePRUrl, reviewRow, slackSeenLine } from './reviewView'

const review = (over: Partial<Review>): Review => ({
  id: 'x',
  url: 'https://github.com/o/r/pull/7',
  repo: 'o/r',
  number: 7,
  project: 'p',
  dir: '/d',
  config_dir: '/c',
  pid: 1,
  started: '2026-09-14T10:00:00Z',
  state: 'done',
  ...over,
})

describe('reviewView', () => {
  it('parsePRUrl accepts a PR URL with anything after the number, nothing else', () => {
    expect(parsePRUrl('https://github.com/orbit-org/app.orbit/pull/1003/changes')).toEqual({
      repo: 'orbit-org/app.orbit',
      number: 1003,
    })
    expect(parsePRUrl(' https://github.com/o/r/pull/7 ')).toEqual({ repo: 'o/r', number: 7 })
    expect(parsePRUrl('https://github.com/o/r/issues/7')).toBeNull()
    expect(parsePRUrl('o/r#7')).toBeNull()
  })

  it('reviewRow words the state, the label and the duration', () => {
    const now = new Date('2026-09-14T11:30:00Z')
    const done = reviewRow(review({ finished: '2026-09-14T10:12:00Z' }), now)
    expect(done.glyph).toBe('✓')
    expect(done.label).toBe('o/r#7')
    expect(done.duration).toBe('12m')
    const running = reviewRow(review({ state: 'running' }), now)
    expect(running.glyph).toBe('▶')
    expect(running.duration).toBe('1h 30m')
    expect(reviewRow(review({ state: 'error' }), now).glyph).toBe('✗')
    expect(durationText(-5)).toBe('0m')
  })

  it('approveView offers approve on a done review and words what happened', () => {
    const now = new Date('2026-09-14T11:30:00Z')
    const slack = {
      workspace: 'orbit',
      channel: 'C1',
      ts: '1.2',
      thread_ts: '1.2',
      server: 'slack-work',
    }
    const clean = approveView(review({ no_issues: true, slack }), now)
    expect(clean.offer).toBe(true)
    expect(clean.label).toBe('approve on GitHub')
    expect(clean.warning).toBe('')
    expect(clean.question).toBe(
      'Approve o/r#7 on GitHub and react ✅ on the Slack message? Only an approve, no comment.',
    )
    const issues = approveView(review({}), now)
    expect(issues.label).toBe('approve anyway')
    expect(issues.warning).toMatch(/did not say "No issues found"/)
    expect(issues.question).not.toMatch(/Slack/)
    expect(approveView(review({ state: 'running' }), now).offer).toBe(false)
    const half = approveView(
      review({ approved: '2026-09-14T11:00:00Z', slack, slack_react_error: 'not_in_channel' }),
      now,
    )
    expect(half.offer).toBe(false)
    expect(half.retryReaction).toBe(true)
    expect(half.status[1]).toBe('Slack ✅ failed: not_in_channel')
    expect(approveView(review({ approve_error: 'own PR' }), now).status).toEqual([
      'approve failed: own PR',
    ])
  })

  it('slackSeenLine words the 👀 the start puts on the Slack message, in any state', () => {
    const slack = {
      workspace: 'orbit',
      channel: 'C1',
      ts: '1.2',
      thread_ts: '1.2',
      server: 'slack-work',
    }
    expect(slackSeenLine(review({ state: 'running', slack }))).toBe(
      '👀 going on the Slack message…',
    )
    expect(
      slackSeenLine(review({ state: 'running', slack, slack_seen: '2026-09-14T11:00:00Z' })),
    ).toBe('👀 added on the Slack message')
    expect(slackSeenLine(review({ slack, slack_seen_error: 'not_in_channel' }))).toBe(
      'Slack 👀 failed: not_in_channel',
    )
    expect(slackSeenLine(review({ state: 'running' }))).toBe('')
  })
})
