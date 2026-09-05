import { describe, expect, it } from 'vitest'

import { ageLabel, clockLabel } from './ageLabel'

describe('ageLabel', () => {
  it('renders null as unknown, never as a number', () => {
    expect(ageLabel(null)).toBe('since ?')
    expect(ageLabel(undefined)).toBe('since ?')
  })
  it('picks the coarsest whole unit', () => {
    expect(ageLabel(0)).toBe('<1m')
    expect(ageLabel(59)).toBe('<1m')
    expect(ageLabel(60)).toBe('1m')
    expect(ageLabel(3599)).toBe('59m')
    expect(ageLabel(3600)).toBe('1h')
    expect(ageLabel(86399)).toBe('23h')
    expect(ageLabel(86400)).toBe('1d')
    expect(ageLabel(2402261)).toBe('27d')
    expect(ageLabel(-5)).toBe('in the future')
  })
})

describe('clockLabel', () => {
  it('formats HH:MM locally and tolerates garbage', () => {
    const d = new Date(2026, 8, 5, 9, 7)
    expect(clockLabel(d.toISOString())).toBe('09:07')
    expect(clockLabel(undefined)).toBe('')
    expect(clockLabel('nope')).toBe('')
  })
})
