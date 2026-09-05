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
      className="grid gap-1 border-b py-3 md:grid-cols-[12rem_1fr] md:gap-4"
    >
      <h2 className="text-sm font-semibold">{label}</h2>
      <div className="space-y-1 text-sm">
        {children}
        {note && <p className="text-xs text-gray-500">{note}</p>}
      </div>
    </section>
  )
}
