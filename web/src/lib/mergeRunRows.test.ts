import { describe, expect, it } from 'vitest'

import type { RunRow } from '../api/types'
import { mergeRunRows } from './mergeRunRows'

const row = (project: string, remote?: string): RunRow => ({
  project,
  remote,
  run: { done: 0, total: 0 },
  acceptance: {},
})

describe('mergeRunRows', () => {
  it('returns local rows alone before any remote fetch', () => {
    expect(mergeRunRows([row('a')], undefined)).toEqual([row('a')])
  })
  it('appends only the remote-tagged rows of a ?remote=1 result', () => {
    const merged = mergeRunRows([row('a')], [row('a'), row('b', 'vps'), row('c', 'vps')])
    expect(merged.map((r) => `${r.remote ?? ''}/${r.project}`)).toEqual(['/a', 'vps/b', 'vps/c'])
  })
})
