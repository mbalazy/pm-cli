import { describe, expect, it } from 'vitest'

import type { ShiftReport, ShiftSummary, SoloReportResult } from '../api/types'
import {
  beforePRLine,
  countsText,
  digestOf,
  digestView,
  leadOf,
  meaningful,
  outcomeChips,
  outcomeView,
  reportTitle,
  unfinished,
} from './soloReportView'

const summary = (over: Partial<ShiftSummary> = {}): ShiftSummary => ({
  done: 0,
  partial: 0,
  not_done: 0,
  parked: 0,
  untouched: 0,
  ...over,
})

const digest = (over: Partial<ShiftReport> = {}): ShiftReport => ({
  tasks: [],
  decisions: [],
  ideas: [],
  ...over,
})

const result = (over: Partial<SoloReportResult> = {}): SoloReportResult => ({
  shift: {
    project: 'a',
    id: 'x',
    kind: 'solo',
    date: '2026-09-14',
    open: false,
    status_line: '',
    tasks: [],
    file: '/f.md',
  },
  kind: 'report',
  markdown: '# r',
  ...over,
})

describe('outcomes', () => {
  it('words each outcome with a glyph and a tone, unknown as no verdict', () => {
    expect(outcomeView('done')).toEqual({ glyph: '✓', label: 'done', tone: 'ok' })
    expect(outcomeView('not_done').tone).toBe('crit')
    expect(outcomeView(undefined).label).toBe('no verdict')
  })

  it('counts without zeros, in a fixed order, and says when a task came back unfinished', () => {
    const s = summary({ untouched: 1, done: 6, parked: 2 })
    expect(outcomeChips(s)).toEqual([
      { label: '6 done', tone: 'ok' },
      { label: '2 parked', tone: 'warn' },
      { label: '1 untouched', tone: '' },
    ])
    expect(countsText(s)).toBe('6 done · 2 parked · 1 untouched')
    expect(unfinished(s)).toBe(true)
    expect(unfinished(summary({ done: 3, untouched: 1 }))).toBe(false)
    expect(countsText(undefined)).toBe('')
  })
})

describe('short lines', () => {
  it('leads with the state minus its verdict word, cut at a sentence or a word', () => {
    expect(leadOf('**zrobione**. Ekran pokazuje `dług`.\n\nPod nim lista.')).toBe(
      'Ekran pokazuje dług.',
    )
    expect(leadOf('zrobione. Nad listą jest podsumowanie:\n- zdrowie;\n- zapadalność')).toBe(
      'Nad listą jest podsumowanie:',
    )
    expect(leadOf('nie ruszone, bo czeka.')).toBe('Nie ruszone, bo czeka.')
    const long = `${'Pierwsze zdanie jest dość długie i ma sens. '.repeat(3)}Reszta.`
    expect(leadOf(long, 100)).toBe(
      'Pierwsze zdanie jest dość długie i ma sens. Pierwsze zdanie jest dość długie i ma sens.',
    )
    expect(leadOf('słowo '.repeat(40), 50)).toBe(`Słowo${' słowo'.repeat(7)}…`)
    expect(leadOf(undefined)).toBe('')
  })

  it('says nothing for "nic." and counts the points left before the PR', () => {
    expect(meaningful('nic.')).toBe(false)
    expect(meaningful('**Brak.**')).toBe(false)
    expect(meaningful('scalić')).toBe(true)
    expect(beforePRLine('nic.')).toBe('')
    expect(beforePRLine('scalić po kroku 1. Decyzje:\n  - A.\n  - B.')).toBe(
      'Scalić po kroku 1. Decyzje: (+2 points)',
    )
    expect(beforePRLine('scalić po kroku 1.')).toBe('Scalić po kroku 1.')
  })
})

describe('digestView', () => {
  it('orders the move, the task cards and the folds, leaving empty parts out', () => {
    const v = digestView(
      digest({
        next: 'Od Ciebie: wypchnij.',
        summary: 'Dwa zrobione.',
        tasks: [
          {
            heading: 'krok 1',
            outcome: 'done',
            problem: 'brak danych.',
            state: '**zrobione**. Są typy.',
            before_pr: 'nic.',
          },
          { heading: 'krok 2', outcome: 'untouched', state: 'nie ruszone, bo czeka.' },
        ],
        decisions: ['A.', 'B.'],
        cleanup: 'Brak.',
        technical: '`abc` commit',
      }),
      '# whole',
    )
    expect(v.next).toBe('Od Ciebie: wypchnij.')
    expect(v.summary).toBe('Dwa zrobione.')
    expect(v.tasks.map((t) => [t.heading, t.outcome.label, t.lead, t.beforePR])).toEqual([
      ['krok 1', 'done', 'Są typy.', ''],
      ['krok 2', 'untouched', 'Nie ruszone, bo czeka.', ''],
    ])
    expect(v.tasks[0].parts.map((p) => p.label)).toEqual(['State', 'Problem'])
    expect(v.folds.map((f) => f.label)).toEqual([
      'Decisions made for you (2)',
      'Technical details',
      'Whole report',
    ])
    expect(v.folds[2].md).toBe('# whole')
  })

  it('uses the digest only for a report that split into tasks, and names the shift', () => {
    const withTasks = digest({ title: 'Raport zmiany', tasks: [{ heading: 'h' }] })
    expect(digestOf(result({ digest: withTasks }))).toBe(withTasks)
    expect(digestOf(result({ digest: digest() }))).toBeUndefined()
    expect(digestOf(result({ kind: 'state', digest: withTasks }))).toBeUndefined()
    const named = result({ digest: withTasks })
    expect(reportTitle(named)).toBe('Raport zmiany')
    named.shift.summary = summary({ title: 'ACME-230, trzy ekrany', done: 1 })
    expect(reportTitle(named)).toBe('ACME-230, trzy ekrany')
    expect(reportTitle(result())).toBe('a · 2026-09-14')
  })
})
