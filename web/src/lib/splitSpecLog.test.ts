import { describe, expect, it } from 'vitest'

import { splitSpecLog } from './splitSpecLog'

describe('splitSpecLog', () => {
  it('separates the Spec block from the Log around it', () => {
    const body =
      'intro\n\n<!-- spec:start -->\n## Description\n\nnow\n<!-- spec:end -->\n\n2026-01-01: note\n'
    expect(splitSpecLog(body)).toEqual({
      spec: '## Description\n\nnow',
      log: 'intro\n\n2026-01-01: note',
    })
  })
  it('treats a body without markers as log only', () => {
    expect(splitSpecLog('  plain body  ')).toEqual({ spec: null, log: 'plain body' })
  })
  it('ignores a malformed block (end before start)', () => {
    const body = '<!-- spec:end -->x<!-- spec:start -->'
    expect(splitSpecLog(body)).toEqual({ spec: null, log: body })
  })
  it('yields an empty log when the body is only a Spec', () => {
    expect(splitSpecLog('<!-- spec:start -->\ns\n<!-- spec:end -->')).toEqual({
      spec: 's',
      log: '',
    })
  })
})
