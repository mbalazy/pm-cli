import { describe, expect, it } from 'vitest'

import type { Shift, SoloLaunch } from '../api/types'
import { sessionText, soloFormDefaults, soloInputOf, soloRows } from './soloView'

const shift = (over: Partial<Shift>): Shift => ({
  project: 'a',
  id: 'x',
  kind: 'solo',
  date: '2026-09-10',
  open: false,
  status_line: '',
  tasks: [],
  file: '/f.md',
  ...over,
})

const counts = { done: 0, partial: 0, not_done: 0, parked: 0, untouched: 0 }

describe('soloRows', () => {
  it('words the glyph, the state, the name and the outcome, narrowed to the projects', () => {
    const rows = soloRows(
      [
        shift({ id: 'o', open: true, tasks: [{ id: 'a-1', title: 't', status: 'todo' }] }),
        shift({
          id: 'r',
          report: '/r.md',
          closed: new Date(Date.now() - 2 * 3600_000).toISOString(),
          tasks: [
            { id: 'a-2', title: 't', status: 'todo', outcome: 'done' },
            { id: 'a-3', title: 't' },
          ],
          summary: { ...counts, title: 'ACME-1, ekrany', done: 1, partial: 1 },
        }),
        shift({ id: 'n', summary: { ...counts, done: 2 } }),
        shift({ id: 'b', project: 'b' }),
      ],
      ['a'],
    )
    expect(rows.map((r) => r.glyph)).toEqual(['▶', '✎', '○'])
    expect(rows[0].when).toBe('open')
    expect(rows[1].when).toMatch(/^closed /)
    expect(rows[2].when).toBe('closed')
    expect(rows.map((r) => r.title)).toEqual(['a-1', 'ACME-1, ekrany', ''])
    expect(rows.map((r) => r.outcome)).toEqual(['', '1 done · 1 partial', '2 done'])
    expect(rows.map((r) => r.tone)).toEqual(['', 'warn', 'ok'])
    expect(soloRows(undefined)).toEqual([])
  })
})

const launch = (over: Partial<SoloLaunch>): SoloLaunch => ({
  id: 'ab12cd34',
  project: 'a',
  input: { project: 'a', queue: 'a-7' },
  argv: [],
  cwd: '/r',
  config_dir: '/h/.claude',
  name: 'solo-a',
  started: '2026-09-15T10:00:00+02:00',
  log: '/l',
  state: 'working',
  status: 'busy',
  attach: 'claude attach ab12cd34',
  logs: 'claude logs ab12cd34',
  ...over,
})

describe('soloRows with launches', () => {
  it('joins a launch to its shift by session id, and a launch without a shift is a row of its own', () => {
    const rows = soloRows(
      [shift({ id: 'sess-1', open: true }), shift({ id: 'sess-2' })],
      undefined,
      [
        launch({
          id: 'aa',
          session_id: 'sess-1',
          state: 'blocked',
          waiting_for: 'permission prompt',
        }),
        launch({
          id: 'bb',
          session_id: 'sess-9',
          state: 'starting',
          input: { project: 'a', queue: 'a-8 a-9' },
        }),
        launch({ id: 'cc', state: 'error', error: 'claude exited 1', attach: '' }),
        launch({ id: 'dd', project: 'b', session_id: 'sess-2', state: 'done' }),
      ],
    )
    // The starting orphan first, then the shifts, then the finished orphans
    // (project b's launch of sess-2 is an orphan: the join is per project).
    expect(rows.map((r) => `${r.shift.project}/${r.shift.id}`)).toEqual([
      'a/sess-9',
      'a/sess-1',
      'a/sess-2',
      'a/cc',
      'b/sess-2',
    ])
    expect(rows[0]).toMatchObject({
      glyph: '▶',
      when: 'open',
      title: 'a-8 a-9',
      session: 'starting',
      live: true,
      hasFile: false,
      attach: 'claude attach ab12cd34',
    })
    expect(rows[0].shift.status_line).toMatch(/waiting for the skill/)
    expect(rows[1]).toMatchObject({
      session: 'blocked · permission prompt',
      live: true,
      hasFile: true,
      attach: 'claude attach ab12cd34',
    })
    expect(rows[1].launch?.id).toBe('aa')
    expect(rows[2]).toMatchObject({ launch: undefined, session: '', live: false, attach: '' })
    expect(rows[3]).toMatchObject({
      glyph: '✗',
      when: 'error',
      tone: 'warn',
      session: 'launch failed · claude exited 1',
      live: false,
      hasFile: false,
    })
    // Narrowed to a project, the other project's orphan goes too.
    expect(
      soloRows([], ['b'], [launch({ id: 'dd', project: 'b' }), launch({ id: 'ee' })]).map(
        (r) => r.launch?.id,
      ),
    ).toEqual(['dd'])
  })

  it('words every state', () => {
    expect(sessionText(undefined)).toBe('')
    expect(sessionText(launch({ state: 'blocked' }))).toBe('blocked · needs input')
    expect(sessionText(launch({ state: 'unknown' }))).toBe('unknown · no supervisor row')
    expect(sessionText(launch({ state: 'done' }))).toBe('done')
    expect(sessionText(launch({ state: 'stopped' }))).toBe('stopped')
  })
})

describe('soloInputOf', () => {
  it('trims, leaves empty fields out and parses the caps', () => {
    expect(soloInputOf(soloFormDefaults('a'))).toEqual({ project: 'a', queue: '', model: 'opus' })
    expect(
      soloInputOf({
        project: 'a',
        queue: '  pm-cli-140 ',
        runtime: 'off',
        base: ' main ',
        model: '',
        push: true,
        pr: false,
        max_tasks: '3',
        max_hours: 'x',
      }),
    ).toEqual({
      project: 'a',
      queue: 'pm-cli-140',
      runtime: 'off',
      base: 'main',
      push: true,
      max_tasks: 3,
    })
    expect(soloInputOf({ ...soloFormDefaults('a'), max_hours: '0' }).max_hours).toBeUndefined()
  })
})
