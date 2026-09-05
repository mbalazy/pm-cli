// The severity's ink class, shared by the glyph, the chips and the tables.
const TONE: Record<string, string> = {
  crit: 'sev-crit',
  warn: 'sev-warn',
  info: 'sev-info',
  ok: 'sev-ok',
}

export function toneClass(severity: string | undefined): string {
  return TONE[severity ?? ''] ?? 'sev-none'
}
