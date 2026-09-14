import { describe, expect, it } from 'vitest'

import type { Review } from '../api/types'
import { durationText, parsePRUrl, reviewRow } from './reviewView'

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
    expect(
      parsePRUrl('https://github.com/OrbitOrg/app.orbit/pull/1003/changes'),
    ).toEqual({ repo: 'OrbitOrg/app.orbit', number: 1003 })
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
})
