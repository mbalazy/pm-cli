import { describe, expect, it } from 'vitest'

import { groupHotkeys, groupSearch, isGroupLetter, parseGroupFilter } from './groupFilter'

describe('parseGroupFilter / groupSearch', () => {
  it('reads a string and ignores anything else', () => {
    expect(parseGroupFilter({ g: 'acme' })).toBe('acme')
    expect(parseGroupFilter({})).toBe('')
    expect(parseGroupFilter({ g: ['a'] })).toBe('')
    expect(parseGroupFilter({ g: 3 })).toBe('')
  })
  it('clears the param for the "all" filter', () => {
    expect(groupSearch('')).toEqual({})
    expect(groupSearch('acme')).toEqual({ g: 'acme' })
  })
})

describe('groupHotkeys', () => {
  it('assigns the first free letter of each slug, in order', () => {
    const m = groupHotkeys([
      { slug: 'lumen' },
      { slug: 'ldap' },
      { slug: 'lint' },
      { slug: 'nova' },
      { slug: 'ln' },
    ])
    expect(m.get('l')).toBe('lumen')
    expect(m.get('d')).toBe('ldap')
    expect(m.get('i')).toBe('lint')
    expect(m.get('n')).toBe('nova')
    // 'ln': l and n are taken and nothing is left - no hotkey, no crash.
    expect([...m.values()]).not.toContain('ln')
  })
  it('skips non-letters', () => {
    expect(groupHotkeys([{ slug: '10c-app' }]).get('c')).toBe('10c-app')
  })
  it('isGroupLetter accepts one lowercase letter only', () => {
    expect(isGroupLetter('a')).toBe(true)
    expect(isGroupLetter('A')).toBe(false)
    expect(isGroupLetter('1')).toBe(false)
    expect(isGroupLetter('Enter')).toBe(false)
  })
})
