// The body's two zones (storage.ApplySpec / ExtractSpec): the Spec is the
// text between the markers and is current truth; everything outside is the
// append-only Log. Same rule as the TUI's bodyToDisplayMarkdown.

export const SPEC_START = '<!-- spec:start -->'
export const SPEC_END = '<!-- spec:end -->'

export interface SpecLog {
  /** null = no well-formed Spec block; the whole body is then the log. */
  spec: string | null
  log: string
}

export function splitSpecLog(body: string): SpecLog {
  const start = body.indexOf(SPEC_START)
  const end = body.indexOf(SPEC_END)
  if (start === -1 || end === -1 || end <= start) {
    return { spec: null, log: body.trim() }
  }
  const spec = body.slice(start + SPEC_START.length, end).trim()
  const before = body.slice(0, start).trim()
  const after = body.slice(end + SPEC_END.length).trim()
  const log = [before, after].filter((s) => s !== '').join('\n\n')
  return { spec, log }
}
