import { describe, expect, it } from 'vitest'

import { actionMeta, openTarget } from './rowActions'

describe('rowActions', () => {
  it('open works now, the mutations name their sub, unknown is flagged', () => {
    expect(actionMeta('open')).toEqual({ label: 'open', pending: '' })
    expect(actionMeta('focus_toggle').pending).toBe('')
    expect(actionMeta('mark_seen').pending).toBe('')
    expect(actionMeta('kill').pending).toBe('pm-cli-118-21')
    expect(actionMeta('teleport')).toEqual({ label: 'teleport', pending: 'unknown action' })
  })
  it('open goes to the task, or to the project when the row has none', () => {
    expect(openTarget({ project: 'atlas', task_id: 'atlas-1' })).toEqual({
      to: '/p/$slug/t/$id',
      params: { slug: 'atlas', id: 'atlas-1' },
    })
    expect(openTarget({ project: 'atlas' })).toEqual({ to: '/p/$slug', params: { slug: 'atlas' } })
  })
})
