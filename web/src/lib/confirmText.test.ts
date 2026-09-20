import { describe, expect, it } from 'vitest'

import type { MutationRequest } from '../api/mutations'
import {
  describeAction,
  flagOptions,
  previewOf,
  initialValue,
  isPendingKind,
  requestFor,
  subjectOfTask,
  previewOfSolo,
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
    const p = { kind: 'set_waiting_for' as const, subject: { ...subject, status: 'doing' } }
    const empty = describeAction(p, '')
    expect(empty.field?.kind).toBe('input')
    expect(empty.warning).toMatch(/no reason/)
    expect(empty.sentence).toBe('Moves atlas-158 to waiting.')
    const filled = describeAction(p, ' client answer ')
    expect(filled.warning).toBeUndefined()
    expect(filled.sentence).toBe('Moves atlas-158 to waiting with the reason: client answer.')
  })
  it('waiting on a task already waiting is an edit of the reason, pre-filled', () => {
    const waiting = { ...subject, status: 'waiting', waiting_for: 'client answer' }
    const p = { kind: 'set_waiting_for' as const, subject: waiting }
    expect(initialValue(p)).toBe('client answer')
    const t = describeAction(p, 'client answer')
    expect(t.sentence).toBe('Sets the waiting reason of atlas-158.')
    expect(t.confirmLabel).toBe('save reason')
    expect(t.warning).toBeUndefined()
    const cleared = describeAction(p, '')
    expect(cleared.sentence).toMatch(/Clears the waiting reason/)
    expect(cleared.warning).toMatch(/no reason/)
    expect(requestFor(p, 'client answer')).toMatchObject({
      body: { status: 'waiting', waiting_for: 'client answer' },
    })
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
  it('edit_notes is a project mutation with a textarea', () => {
    const p = {
      kind: 'edit_notes' as const,
      subject: { project: 'acme-api', title: 'acme-api', notes: 'n' },
    }
    expect(initialValue(p)).toBe('n')
    expect(describeAction(p, '').field?.kind).toBe('textarea')
    expect(describeAction(p, '').warning).toMatch(/NOT saved/)
    expect(describeAction(p, 'x').warning).toBeUndefined()
    expect(requestFor(p, 'new')).toEqual({
      kind: 'project',
      project: 'acme-api',
      body: { notes: 'new' },
    })
  })
  it('isPendingKind knows the mutation kinds, not open/report', () => {
    expect(isPendingKind('focus_toggle')).toBe(true)
    expect(isPendingKind('mark_seen')).toBe(true)
    expect(isPendingKind('kill')).toBe(true)
    expect(isPendingKind('release_claim')).toBe(true)
    expect(isPendingKind('open')).toBe(false)
    expect(isPendingKind('report')).toBe(false)
  })
})

describe('project settings actions', () => {
  it('sleep warns and sends archived; wake and move_repo send their field', () => {
    const sleep = { kind: 'sleep_project' as const, subject: { project: 'orbit', title: 'Orbit' } }
    expect(describeAction(sleep, '').warning).toMatch(/leaves every group/)
    expect(requestFor(sleep, '')).toEqual({
      kind: 'project',
      project: 'orbit',
      body: { archived: true },
    })
    const wake = { kind: 'wake_project' as const, subject: { project: 'orbit', title: 'Orbit' } }
    expect(requestFor(wake, '')).toEqual({
      kind: 'project',
      project: 'orbit',
      body: { archived: false },
    })
    const move = {
      kind: 'move_repo' as const,
      subject: { project: 'acme-api', title: 'AcmeApi', group: 'acme' },
    }
    expect(describeAction(move, '').confirmLabel).toBe('move to acme')
    expect(requestFor(move, '')).toEqual({
      kind: 'project',
      project: 'acme-api',
      body: { group: 'acme' },
    })
    const leave = {
      kind: 'move_repo' as const,
      subject: { project: 'acme-api', title: 'AcmeApi', group: '' },
    }
    expect(describeAction(leave, '').confirmLabel).toBe('leave group')
    expect(requestFor(leave, '')).toEqual({
      kind: 'project',
      project: 'acme-api',
      body: { group: '' },
    })
  })
})

describe('run actions', () => {
  const plan = {
    action: 'resume_run',
    project: 'atlas',
    task_id: 'atlas-1',
    kind: 'run-epic',
    argv: ['run-epic', 'atlas', 'atlas-1', '--yolo'],
    cwd: '/r',
    log: '/l',
    warnings: ['dirty tree'],
    additional_avail: true,
  }
  it('the preview is the plan, the flags depend on the action, the request carries the flags', () => {
    const p = {
      kind: 'resume_run' as const,
      subject: { project: 'atlas', task_id: 'atlas-1', title: 'Epic' },
    }
    const t = describeAction(p, '', undefined, plan)
    expect(t.preview).toBe('cd /r && pm run-epic atlas atlas-1 --yolo')
    expect(t.warnings).toEqual(['dirty tree'])
    expect(t.flags?.map((f) => f.key)).toEqual(['yolo', 'additional'])
    expect(t.loading).toBe(false)
    expect(describeAction(p, '').loading).toBe(true)
    expect(requestFor(p, '', { yolo: true })).toEqual({
      kind: 'run',
      project: 'atlas',
      taskId: 'atlas-1',
      action: 'resume_run',
      flags: { yolo: true },
    })
    expect(flagOptions('rerun_finish', { ...plan, additional_avail: false })[0].disabled).toMatch(
      /no worktree/,
    )
    expect(flagOptions('claim')).toEqual([])
    expect(flagOptions('kill')).toEqual([])
    expect(previewOf({ ...plan, argv: undefined, target: 'nothing is running' })).toBe(
      'nothing is running',
    )
    expect(
      describeAction({ kind: 'kill', subject: p.subject }, '', undefined, {
        ...plan,
        action: 'kill',
        argv: undefined,
        pid: 0,
      }).sentence,
    ).toMatch(/already gone/)
    expect(describeAction({ kind: 'rerun_finish', subject: p.subject }, '').sentence).toMatch(
      /never --sim/,
    )
  })
  it('a warning that promises a refusal blocks confirm unless a flag can route around it', () => {
    const p = {
      kind: 'resume_run' as const,
      subject: { project: 'atlas', task_id: 'atlas-1', title: 'Epic' },
    }
    const dirty = 'working tree dirty - the run will refuse unless it uses an additional worktree'
    expect(describeAction(p, '', undefined, plan).blocked).toBeUndefined()
    expect(
      describeAction(p, '', undefined, { ...plan, warnings: [dirty], additional_avail: true })
        .blocked,
    ).toBeUndefined()
    expect(
      describeAction(p, '', undefined, { ...plan, warnings: [dirty], additional_avail: false })
        .blocked,
    ).toMatch(/commit or stash the working tree in \/r/)
    expect(
      describeAction({ kind: 'rerun_finish', subject: p.subject }, '', undefined, {
        ...plan,
        action: 'rerun_finish',
        warnings: ['claim held by x - `pm finish` will refuse'],
      }).blocked,
    ).toMatch(/release or wait out the claim/)
  })
})

describe('solo actions', () => {
  const input = {
    project: 'pm-cli',
    queue: 'pm-cli-140',
    runtime: 'off',
    base: 'main',
    model: 'opus',
  }
  const plan = {
    project: 'pm-cli',
    input,
    exe: '/usr/local/bin/claude',
    argv: [
      '--dangerously-skip-permissions',
      '--autocompact',
      '400k',
      '--settings',
      '{"worktree":{"bgIsolation":"none"}}',
      '--model',
      'opus',
      '--name',
      'solo-pm-cli',
      '--bg',
      '/solo pm-cli-140 --no-runtime --base main',
    ],
    prompt: '/solo pm-cli-140 --no-runtime --base main',
    cwd: '/repos/pm-cli',
    config_dir: '/home/u/.claude',
    name: 'solo-pm-cli',
    warnings: ['3 uncommitted changes in the checkout'],
    ask_rules: 0,
  }
  it('the preview is the quoted launch line, loading until the plan, the request is the input', () => {
    const p = {
      kind: 'solo_start' as const,
      subject: { project: 'pm-cli', title: 'pm-cli-140', solo: input },
    }
    const t = describeAction(p, '', undefined, undefined, plan)
    expect(t.heading).toBe('solo · pm-cli-140')
    expect(t.preview).toBe(
      'cd /repos/pm-cli && claude --dangerously-skip-permissions --autocompact 400k --settings \'{"worktree":{"bgIsolation":"none"}}\' --model opus --name solo-pm-cli --bg \'/solo pm-cli-140 --no-runtime --base main\'',
    )
    expect(t.warnings).toEqual(['3 uncommitted changes in the checkout'])
    expect(t.loading).toBe(false)
    expect(t.confirmLabel).toBe('launch solo')
    expect(t.sentence).toMatch(/claude --bg/)
    expect(describeAction(p, '').loading).toBe(true)
    expect(requestFor(p, '')).toEqual({ kind: 'solo_start', input })
    // A non-default config dir is spelled out, so the line is pasteable as is.
    expect(previewOfSolo({ ...plan, config_dir: '/home/u/.claude-work' })).toMatch(
      /^cd \/repos\/pm-cli && CLAUDE_CONFIG_DIR=\/home\/u\/.claude-work claude /,
    )
    expect(previewOfSolo(undefined)).toBe('')
  })
  it('stop names the claude stop command and how the session comes back', () => {
    const launch = {
      id: 'ab12cd34',
      project: 'pm-cli',
      input,
      argv: [],
      cwd: '/repos/pm-cli',
      config_dir: '/home/u/.claude',
      name: 'solo-pm-cli',
      started: '2026-09-15T10:00:00+02:00',
      log: '/l',
      state: 'working',
      attach: 'claude attach ab12cd34',
      logs: 'claude logs ab12cd34',
    }
    const p = {
      kind: 'solo_stop' as const,
      subject: { project: 'pm-cli', title: 'solo-pm-cli', launch },
    }
    const t = describeAction(p, '')
    expect(t.heading).toBe('solo · solo-pm-cli')
    expect(t.sentence).toMatch(/claude stop ab12cd34/)
    expect(t.sentence).toMatch(/claude attach ab12cd34/)
    expect(t.confirmLabel).toBe('stop solo')
    expect(t.loading).toBeFalsy()
    expect(requestFor(p, '')).toEqual({ kind: 'solo_stop', id: 'ab12cd34' })
  })
})
