import { describe, expect, it } from 'vitest'

import type { ChangeEvent } from '../api/types'
import { visibleEvents } from './changesView'

const ev = (id: string, seen: boolean) => ({ id, seen }) as unknown as ChangeEvent

describe('visibleEvents', () => {
  it('hides seen events unless asked to show them', () => {
    const events = [ev('a', false), ev('b', true), ev('c', false)]
    expect(visibleEvents(events, false).map((e) => e.id)).toEqual(['a', 'c'])
    expect(visibleEvents(events, true).map((e) => e.id)).toEqual(['a', 'b', 'c'])
  })
})
