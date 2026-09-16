import type { Shift, SoloInput, SoloLaunch } from '../api/types'
import { relativeTime } from './relativeTime'
import { countsText, unfinished } from './soloReportView'

// The Runs screen's solo table (pm-cli-136): one line per /solo shift, open
// ones first (the API's order), narrowed to a group's projects when asked.

export interface SoloRow {
  shift: Shift
  /** ▶ open, ✎ closed with a report, ○ closed without one. */
  glyph: string
  /** "open" or "closed <relative time>". */
  when: string
  /** The report's name for the shift, else the queued task ids. */
  title: string
  /** "6 done · 1 untouched"; '' until a report or the Progress says anything. */
  outcome: string
  /** warn when a task came back unfinished, ok when something got done. */
  tone: string
  /** The cockpit's launch record of this shift's session, when it started one. */
  launch?: SoloLaunch
  /** The session's state in words: "working", "blocked · permission prompt", ''. */
  session: string
  /** True while the session is starting, working or blocked (a stop makes sense). */
  live: boolean
  /** The command to paste in a terminal, '' without a launch. */
  attach: string
  /** False for a launch the skill has not written a shift for yet (no report to open). */
  hasFile: boolean
}

/** The session state a launch carries, worded for a table cell. */
export function sessionText(l: SoloLaunch | undefined): string {
  if (!l) return ''
  switch (l.state) {
    case 'blocked':
      return l.waiting_for ? `blocked · ${l.waiting_for}` : 'blocked · needs input'
    case 'error':
      return `launch failed · ${l.error ?? ''}`.trim()
    case 'unknown':
      return l.waiting_for ? `unknown · ${l.waiting_for}` : 'unknown · no supervisor row'
    default:
      return l.state
  }
}

/**
 * One row per shift the skill wrote, joined with the cockpit's launch of the
 * same session (shift id = session id); a launch whose shift does not
 * exist yet (the skill's Step 0 has not run, or refused) is a row of its
 * own, so the table shows the session seconds after the click.
 */
export function soloRows(
  shifts: Shift[] | undefined,
  projects?: string[],
  launches?: SoloLaunch[],
): SoloRow[] {
  const keep = projects ? new Set(projects) : undefined
  const byShift = new Map<string, SoloLaunch>()
  for (const l of launches ?? []) if (l.session_id) byShift.set(`${l.project}/${l.session_id}`, l)
  const seen = new Set<SoloLaunch>()
  const rows: SoloRow[] = (shifts ?? [])
    .filter((s) => !keep || keep.has(s.project))
    .map((s) => {
      const launch = byShift.get(`${s.project}/${s.id}`)
      if (launch) seen.add(launch)
      return {
        shift: s,
        glyph: s.open ? '▶' : s.report ? '✎' : '○',
        when: s.open ? 'open' : s.closed ? `closed ${relativeTime(s.closed)}` : 'closed',
        title: s.summary?.title || s.tasks.map((t) => t.id).join(', '),
        outcome: countsText(s.summary),
        tone: unfinished(s.summary) ? 'warn' : s.summary?.done ? 'ok' : '',
        launch,
        session: sessionText(launch),
        live: isLive(launch),
        attach: launch?.attach ?? '',
        hasFile: true,
      }
    })
  const orphans = (launches ?? [])
    .filter((l) => !seen.has(l) && (!keep || keep.has(l.project)))
    .map((l): SoloRow => {
      const live = isLive(l)
      return {
        shift: {
          project: l.project,
          id: l.session_id || l.id,
          kind: 'launch',
          date: l.started.slice(0, 10),
          open: live,
          status_line: live
            ? 'launched from the cockpit - waiting for the skill to open the shift'
            : 'launched from the cockpit - no shift file',
          tasks: [],
          file: '',
        },
        glyph: live ? '▶' : l.state === 'error' || l.state === 'failed' ? '✗' : '○',
        when: live ? 'open' : l.state,
        title: l.input.queue,
        outcome: '',
        tone: l.state === 'error' || l.state === 'failed' ? 'warn' : '',
        launch: l,
        session: sessionText(l),
        live,
        attach: l.attach,
        hasFile: false,
      }
    })
  // Live launches first (they are happening now), then the API's order.
  return [...orphans.filter((r) => r.live), ...rows, ...orphans.filter((r) => !r.live)]
}

function isLive(l: SoloLaunch | undefined): boolean {
  return (
    l !== undefined && (l.state === 'starting' || l.state === 'working' || l.state === 'blocked')
  )
}

/** The launch form as the user types it (numbers as text, so a cleared field is ''). */
export interface SoloForm {
  project: string
  queue: string
  runtime: string
  base: string
  model: string
  push: boolean
  pr: boolean
  max_tasks: string
  max_hours: string
}

export const SOLO_RUNTIMES: { value: string; label: string }[] = [
  { value: '', label: 'the skill decides' },
  { value: 'sim', label: 'simulator (--sim)' },
  { value: 'web', label: 'browser (--web)' },
  { value: 'off', label: 'off (--no-runtime)' },
]

export function soloFormDefaults(project = ''): SoloForm {
  return {
    project,
    queue: '',
    runtime: '',
    base: '',
    model: 'opus',
    push: false,
    pr: false,
    max_tasks: '',
    max_hours: '',
  }
}

/** The form -> the request: trimmed, empty fields left out, numbers parsed (a non-number = left out). */
export function soloInputOf(f: SoloForm): SoloInput {
  const num = (s: string) => {
    const n = Number.parseInt(s.trim(), 10)
    return Number.isFinite(n) && n > 0 ? n : undefined
  }
  const input: SoloInput = { project: f.project, queue: f.queue.trim() }
  if (f.runtime) input.runtime = f.runtime
  if (f.base.trim()) input.base = f.base.trim()
  if (f.model.trim()) input.model = f.model.trim()
  if (f.push) input.push = true
  if (f.pr) input.pr = true
  const mt = num(f.max_tasks)
  if (mt) input.max_tasks = mt
  const mh = num(f.max_hours)
  if (mh) input.max_hours = mh
  return input
}
