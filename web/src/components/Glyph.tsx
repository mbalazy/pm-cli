import { cn } from '@/components/ui/cn'

// A state glyph in its severity's ink. The glyph is the information (it
// survives a monochrome screen); the colour only repeats it. The accessible
// label is the caller's - "severity: crit", "worst: warn", "quiet".

interface Props {
  glyph: string
  severity?: string
  label?: string
  className?: string
}

const TONE: Record<string, string> = {
  crit: 'sev-crit',
  warn: 'sev-warn',
  info: 'sev-info',
  ok: 'sev-ok',
}

export function toneClass(severity: string | undefined): string {
  return TONE[severity ?? ''] ?? 'sev-none'
}

export function Glyph({ glyph, severity, label, className }: Props) {
  return (
    <span
      aria-label={label}
      className={cn(
        'inline-block w-4 text-center font-mono text-sm',
        toneClass(severity),
        className,
      )}
    >
      {glyph}
    </span>
  )
}
