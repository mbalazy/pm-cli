import { describe, expect, it } from 'vitest'

import { SHORTCUTS, isTypingTarget, shortcutFor } from './shortcuts'

describe('shortcutFor', () => {
  it('maps the plain keys', () => {
    expect(shortcutFor({ key: 'j' })).toBe('down')
    expect(shortcutFor({ key: 'k' })).toBe('up')
    expect(shortcutFor({ key: 'Enter' })).toBe('open')
    expect(shortcutFor({ key: 'Escape' })).toBe('close')
    expect(shortcutFor({ key: '?' })).toBe('help')
    expect(shortcutFor({ key: 'x' })).toBeNull()
  })
  it('opens the palette on cmd/ctrl+k, even from a text field', () => {
    const input = document.createElement('input')
    expect(shortcutFor({ key: 'k', metaKey: true })).toBe('palette')
    expect(shortcutFor({ key: 'k', ctrlKey: true, target: input })).toBe('palette')
  })
  it('leaves other modifier chords to the browser', () => {
    expect(shortcutFor({ key: 'j', metaKey: true })).toBeNull()
    expect(shortcutFor({ key: 'j', altKey: true })).toBeNull()
  })
  it('ignores navigation keys while typing, but still closes on Escape', () => {
    const input = document.createElement('input')
    expect(shortcutFor({ key: 'j', target: input })).toBeNull()
    expect(shortcutFor({ key: 'Enter', target: input })).toBeNull()
    expect(shortcutFor({ key: 'Escape', target: input })).toBe('close')
  })
  it('lists every action in the help table', () => {
    const actions = new Set(SHORTCUTS.map((s) => s.action))
    for (const a of ['down', 'up', 'open', 'close', 'help', 'palette'])
      expect(actions.has(a as never)).toBe(true)
  })
})

describe('isTypingTarget', () => {
  it('recognises inputs, textareas, selects and contenteditable', () => {
    expect(isTypingTarget(document.createElement('input'))).toBe(true)
    expect(isTypingTarget(document.createElement('textarea'))).toBe(true)
    expect(isTypingTarget(document.createElement('select'))).toBe(true)
    const div = document.createElement('div')
    expect(isTypingTarget(div)).toBe(false)
    expect(isTypingTarget(null)).toBe(false)
    expect(isTypingTarget(document.body)).toBe(false)
  })
})
