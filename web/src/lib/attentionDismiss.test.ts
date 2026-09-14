import { describe, expect, it } from 'vitest'

import type { AttentionRow } from '../api/types'
import { DISMISS_OLDER_DAYS, dismissRowOf, olderThan } from './attentionView'
import { isDirectAction, reportTarget } from './rowActions'

const row = (over: Partial<AttentionRow>): AttentionRow => ({
  section: 'needs_me',
  severity: 'warn',
  project: 'p',
  group: 'p',
  task_id: 'p-1',
  title: 't',
  reason: 'r',
  age_seconds: null,
  actions: ['open', 'dismiss'],
  ...over,
})

describe('dismiss helpers', () => {
  it('olderThan takes dismissable rows of a known age past the threshold only', () => {
    const day = 86400
    const rows = [
      row({ task_id: 'old', age_seconds: 20 * day }),
      row({ task_id: 'edge', age_seconds: DISMISS_OLDER_DAYS * day }),
      row({ task_id: 'fresh', age_seconds: day }),
      row({ task_id: 'unknown', age_seconds: null }),
      row({ task_id: 'nodismiss', age_seconds: 30 * day, actions: ['open'] }),
    ]
    expect(olderThan(rows, DISMISS_OLDER_DAYS).map((r) => r.task_id)).toEqual(['old', 'edge'])
  })

  it('dismissRowOf names the row by section, project, id and stamp', () => {
    expect(dismissRowOf(row({ since: 's' }))).toEqual({
      section: 'needs_me',
      project: 'p',
      task_id: 'p-1',
      shift: undefined,
      since: 's',
    })
  })

  it('dismiss skips the dialog; open_report goes to the shift page', () => {
    expect(isDirectAction('dismiss')).toBe(true)
    expect(isDirectAction('kill')).toBe(false)
    expect(reportTarget({ project: 'p', shift: 'abc' })).toEqual({
      to: '/solo/$project/$shift',
      params: { project: 'p', shift: 'abc' },
    })
  })
})
