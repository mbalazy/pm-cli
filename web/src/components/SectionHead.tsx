import type { ReactNode } from 'react'

// A section head of the ledger: the title in the paper's serif, the count in
// tabular figures, the "how it is counted" line in the margin, and room for
// a control on the right. The wording is the caller's.

interface Props {
  title: string
  count?: ReactNode
  why?: string
  /** Right-aligned controls (a button, a toggle). */
  children?: ReactNode
  /** Heading level - 2 by default, 3 inside a section. */
  level?: 2 | 3
}

export function SectionHead({ title, count, why, children, level = 2 }: Props) {
  const H = level === 3 ? 'h3' : 'h2'
  return (
    <H className="mb-1.5 flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-rule-strong pb-1">
      <span className="display text-[1.05rem] leading-tight">{title}</span>
      {count !== undefined && count !== '' && <span className="num text-ink-2">{count}</span>}
      {why && <span className="text-xs text-ink-3 italic">{why}</span>}
      {children && (
        <span className="ml-auto flex items-center gap-2 text-sm font-normal">{children}</span>
      )}
    </H>
  )
}
