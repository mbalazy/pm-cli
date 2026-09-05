import { describe, expect, it } from 'vitest'

import { COUNTER_COLUMNS, rowGlyph, severityGlyph } from './glyphs'

describe('glyphs', () => {
  it('maps severities and tolerates an unknown one', () => {
    expect(severityGlyph('crit')).toBe('✗')
    expect(severityGlyph('ok')).toBe('○')
    expect(severityGlyph('weird')).toBe('·')
  })
  it('lets the section override the severity where it has a symbol', () => {
    expect(rowGlyph({ section: 'waiting', severity: 'warn' })).toBe('⧗')
    expect(rowGlyph({ section: 'stuck_projects', severity: 'warn' })).toBe('◔')
    expect(rowGlyph({ section: 'needs_me', severity: 'crit' })).toBe('✗')
    expect(rowGlyph({ section: 'needs_me', severity: 'warn' })).toBe('▲')
  })
  it('has the four counter columns in the wireframe order', () => {
    expect(COUNTER_COLUMNS.map((c) => c.key)).toEqual(['failed', 'visual', 'waiting', 'quiet'])
  })
})
