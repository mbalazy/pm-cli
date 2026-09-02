import { describe, expect, it } from 'vitest'

import { acceptCellText, runCellText } from './runCells'

describe('runCellText', () => {
  it('matches storage.RunCell.String', () => {
    expect(runCellText({ done: 0, total: 0 })).toBe('-')
    expect(runCellText({ state: 'prepped', done: 0, total: 3 })).toBe('prepped')
    expect(runCellText({ state: 'running', done: 0, total: 0 })).toBe('running')
    expect(runCellText({ state: 'stale', done: 2, total: 8 })).toBe('stale 2/8')
  })
})

describe('acceptCellText', () => {
  it('matches storage.AcceptCell.String', () => {
    expect(acceptCellText({})).toBe('-')
    expect(acceptCellText({ state: 'done' })).toBe('done')
    expect(acceptCellText({ state: 'claimed', host: 'mac', age: '3m' })).toBe('claimed (mac, 3m)')
    expect(acceptCellText({ state: 'claimed', host: 'mac' })).toBe('claimed (mac)')
    expect(acceptCellText({ state: 'running', age: '10m' })).toBe('running (10m)')
    expect(acceptCellText({ state: 'done', visual_claims_open: 2 })).toBe(
      'done, 2 visual claim(s) open',
    )
  })
})
