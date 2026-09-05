import type { ReactNode } from 'react'

// One labelled row of the settings screen: the label on the left, the
// controls on the right, an optional one-line note under them. Layout only.

interface Props {
  label: string
  note?: string
  children: ReactNode
}

export function SettingsGroup({ label, note, children }: Props) {
  return (
    <section
      aria-label={label}
      className="grid gap-2 border-b border-rule py-4 md:grid-cols-[12rem_minmax(0,1fr)] md:gap-6"
    >
      <h2 className="display text-[1.05rem] leading-snug">{label}</h2>
      <div className="space-y-2 text-sm">
        {children}
        {note && <p className="max-w-[68ch] text-xs text-ink-3 italic">{note}</p>}
      </div>
    </section>
  )
}
