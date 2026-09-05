import { describe, expect, it } from 'vitest'

import type { MutationRequest } from '../api/mutations'
import {
  describeAction,
  initialValue,
  isPendingKind,
  requestFor,
  subjectOfTask,
} from './confirmText'

const body = (r: MutationRequest) => (r.kind === 'task' ? r.body : undefined)

const subject = {
  project: 'atlas',
  task_id: 'atlas-158',
  title: 'ACME-1736',
  status: 'waiting',
  waiting_for: 'user',
  brief: 'old brief',
  since: '2026-09-05T10:00:00Z',
}

describe('describeAction', () => {
  it('focus toggle words both directions', () => {
    const p = { kind: 'focus_toggle' as const, subject }
    expect(describeAction(p, '', false).sentence).toMatch(/Puts atlas-158 on today's focus/)
    expect(describeAction(p, '', true).confirmLabel).toBe('drop from focus')
    expect(describeAction(p, '', false).field).toBeUndefined()
    expect(describeAction(p, '').heading).toBe('atlas-158 ACME-1736')
  })
  it('waiting needs a reason and warns when empty', () => {
    const p = { kind: 'set_waiting_for' as const, subject }
    const empty = describeAction(p, '')
    expect(empty.field?.kind).toBe('input')
    expect(empty.warning).toMatch(/no reason/)
    expect(empty.sentence).toBe('Moves atlas-158 to waiting.')
    const filled = describeAction(p, ' client answer ')
    expect(filled.warning).toBeUndefined()
    expect(filled.sentence).toBe('Moves atlas-158 to waiting with the reason: client answer.')
  })
  it('set_status warns only for waiting', () => {
    const w = describeAction({ kind: 'set_status', subject, status: 'waiting' }, '')
    expect(w.field).toBeDefined()
    expect(w.warning).toMatch(/no reason/)
    const d = describeAction({ kind: 'set_status', subject, status: 'done' }, '')
    expect(d.field).toBeUndefined()
    expect(d.warning).toBeUndefined()
    expect(d.sentence).toBe('Moves atlas-158 from waiting to done.')
  })
  it('a project row (no task id) heads with the title', () => {
    const t = describeAction(
      { kind: 'mark_seen', subject: { project: 'p', title: 'p', since: 'T' } },
      '',
    )
    expect(t.heading).toBe('p')
    expect(t.sentence).toMatch(/up to this one \(T\)/)
  })
  it('edit brief is a textarea pre-filled with the current brief', () => {
    const p = { kind: 'edit_brief' as const, subject }
    expect(initialValue(p)).toBe('old brief')
    expect(describeAction(p, '').field?.kind).toBe('textarea')
    expect(describeAction(p, '').warning).toMatch(/clears/)
    expect(initialValue({ kind: 'edit_waiting_for', subject })).toBe('user')
    expect(initialValue({ kind: 'set_status', subject, status: 'waiting' })).toBe('user')
    expect(initialValue({ kind: 'set_status', subject, status: 'done' })).toBe('')
    expect(initialValue({ kind: 'focus_toggle', subject })).toBe('')
  })
})

describe('requestFor', () => {
  it('maps every kind to its endpoint and body', () => {
    expect(requestFor({ kind: 'focus_toggle', subject }, '')).toEqual({
      kind: 'focus',
      project: 'atlas',
      taskId: 'atlas-158',
    })
    expect(requestFor({ kind: 'set_waiting_for', subject }, ' review ')).toEqual({
      kind: 'task',
      project: 'atlas',
      taskId: 'atlas-158',
      body: { status: 'waiting', waiting_for: 'review' },
    })
    expect(body(requestFor({ kind: 'back_to_todo', subject }, ''))).toEqual({
      status: 'todo',
      waiting_for: '',
    })
    expect(requestFor({ kind: 'mark_seen', subject }, '')).toEqual({
      kind: 'seen',
      ts: '2026-09-05T10:00:00Z',
    })
    expect(body(requestFor({ kind: 'edit_brief', subject }, 'new\nbrief'))).toEqual({
      brief: 'new\nbrief',
    })
    expect(body(requestFor({ kind: 'edit_waiting_for', subject }, ''))).toEqual({ waiting_for: '' })
    expect(body(requestFor({ kind: 'set_status', subject, status: 'done' }, 'x'))).toEqual({
      status: 'done',
    })
    expect(body(requestFor({ kind: 'set_status', subject, status: 'waiting' }, 'x'))).toEqual({
      status: 'waiting',
      waiting_for: 'x',
    })
  })
  it('subjectOfTask renames id to task_id and keeps the editable fields', () => {
    expect(
      subjectOfTask({ project: 'p', id: 'p-1', title: 'T', status: 'todo', brief: 'b' }),
    ).toEqual({
      project: 'p',
      task_id: 'p-1',
      title: 'T',
      status: 'todo',
      waiting_for: undefined,
      brief: 'b',
    })
  })
  it('isPendingKind knows the batch-1 actions only', () => {
    expect(isPendingKind('focus_toggle')).toBe(true)
    expect(isPendingKind('mark_seen')).toBe(true)
    expect(isPendingKind('kill')).toBe(false)
    expect(isPendingKind('open')).toBe(false)
  })
})
