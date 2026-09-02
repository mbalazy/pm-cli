import { describe, expect, it } from 'vitest'

import { pushRecent } from './recent'

const t = (id: string) => ({ project: 'p', id, title: id })

describe('pushRecent', () => {
  it('prepends, dedupes and caps', () => {
    expect(pushRecent([t('a'), t('b')], t('b'))).toEqual([t('b'), t('a')])
    expect(pushRecent([t('a')], t('c'), 1)).toEqual([t('c')])
  })
  it('does not mutate the input', () => {
    const input = [t('a')]
    pushRecent(input, t('b'))
    expect(input).toEqual([t('a')])
  })
})
