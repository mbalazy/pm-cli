import { describe, expect, it } from 'vitest'

import type { AttentionRow, AttentionSection } from '../api/types'
import { SECTION_CAP, capSection, rowKey, sectionMeta } from './attentionView'

const row = (i: number): AttentionRow => ({
  section: 'waiting',
  severity: 'info',
  project: 'p',
  group: 'p',
  task_id: `p-${i}`,
  title: `t${i}`,
  reason: 'r',
  age_seconds: null,
  actions: ['open'],
})
const section = (n: number, total = n): AttentionSection => ({
  name: 'waiting',
  rows: Array.from({ length: n }, (_, i) => row(i)),
  total,
})

describe('sectionMeta', () => {
  it('knows the API sections and falls back to the name', () => {
    expect(sectionMeta('needs_me').title).toBe('Needs me')
    expect(sectionMeta('waiting').empty).toMatch(/waiting/)
    expect(sectionMeta('brand_new')).toEqual({
      title: 'brand_new',
      why: '',
      empty: 'Nothing here.',
    })
  })
})

describe('capSection', () => {
  it('shows the first SECTION_CAP rows and counts the rest as collapsed', () => {
    const c = capSection(section(20), false)
    expect(c.rows).toHaveLength(SECTION_CAP)
    expect(c.rows[0].task_id).toBe('p-0')
    expect(c.collapsed).toBe(20 - SECTION_CAP)
    expect(c.elsewhere).toBe(0)
  })
  it('expanded shows everything the API sent', () => {
    const c = capSection(section(20), true)
    expect(c.rows).toHaveLength(20)
    expect(c.collapsed).toBe(0)
  })
  it("reports the API's own truncation as elsewhere", () => {
    const c = capSection(section(5, 44), true)
    expect(c.rows).toHaveLength(5)
    expect(c.elsewhere).toBe(39)
  })
  it('a short section is untouched', () => {
    const c = capSection(section(2), false)
    expect(c).toEqual({ rows: section(2).rows, collapsed: 0, elsewhere: 0 })
  })
})

describe('rowKey', () => {
  it('distinguishes project rows from task rows', () => {
    expect(rowKey(row(1))).toBe('waiting/p/p-1')
    expect(rowKey({ ...row(1), task_id: undefined })).toBe('waiting/p/')
  })
})
