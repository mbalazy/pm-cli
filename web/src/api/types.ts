// Hand-written mirrors of the JSON the Go side returns. The field names are
// the json tags in internal/service/dto.go (and internal/server/server.go for
// the /api/projects row and /api/focus) - one to one, no renaming, no schema
// generator (decision: simplicity; the Go structs ARE the contract and a
// drifted field shows up as `undefined` in exactly one place).
//
// PERMANENT layer: routes/ and components/ may be replaced wholesale; this
// file, client.ts and queries.ts stay.

/** service.TaskSummary - the listing shape of a task. */
export interface TaskSummary {
  id: string
  title: string
  status: string
  project: string
  /** RFC3339 or a bare YYYY-MM-DD date - pm never migrates files. */
  updated: string
  branch?: string
  parent?: string
  order?: number
  tags?: string[]
  links?: Record<string, string>
  brief?: string
  ac?: string
  waiting_for?: string
  /** Empty/absent = UNKNOWN (older task), never "just now". */
  status_changed?: string
  session_count: number
}

/** service.TaskDetail - GetTask's full shape. */
export interface TaskDetail extends TaskSummary {
  created: string
  body?: string
  sessions?: string[]
  depends_on?: string[]
  mode?: string
  model?: string
  epic_mode?: string
  finish_mode?: string
  runtime?: string
}

/** service.ListTasksResult - a page of tasks plus the truncation footer. */
export interface ListTasksResult {
  tasks: TaskSummary[]
  total: number
  shown: number
  note?: string
}

/** storage.TrackerChild */
export interface TrackerChild {
  id: string
  title: string
  status: string
  order: number
  branch?: string
  brief_line?: string
}

/** storage.Tracker - the generated parent/subtask rollup. */
export interface Tracker {
  id: string
  title: string
  status: string
  brief_line?: string
  total: number
  progress: Record<string, number>
  children?: TrackerChild[]
  children_omitted?: boolean
}

/** storage.JournalCount */
export interface JournalCount {
  name: string
  subject?: string
  total: number
  open: number
}

/** storage.TimelineEntry - one line of a project's timeline. */
export interface TimelineEntry {
  id: string
  ts: string
  /** event | decision | state */
  kind: string
  text: string
  /** Task ids, paths, URLs - free text, never validated. */
  refs?: string[]
  session?: string
}

/** service.TimelineReadResult - /api/timeline/{project}: the latest state and every entry after it. */
export interface TimelineRead {
  project: string
  /** The latest state; null when the project has none yet. */
  state: TimelineEntry | null
  /** Oldest first. With no state: the newest few entries. */
  since: TimelineEntry[]
  /** Time to write a new state (enough entries or days after this one). */
  stale: boolean
  entries_since: number
  days_since: number
  /** Every entry of the timeline, states included; 0 = no timeline. */
  total: number
  note?: string
}

/** service.ProjectMeta */
export interface ProjectMeta {
  slug: string
  name: string
  repo?: string
  stack?: string
  notes?: string
  links?: Record<string, string>
  statuses: string[]
}

/** service.ProjectContextResult - /api/context?project=slug */
export interface ProjectContextResult {
  attention?: AttentionDigest
  attention_note?: string
  doing_tasks: TaskDetail[]
  executor_profile?: string
  focus_tasks?: TaskSummary[]
  journals?: JournalCount[]
  journals_note?: string
  project: ProjectMeta
  task_counts: Record<string, number>
  trackers?: Tracker[]
}

/** service.ProjectSummary - one row of the cross-project context. */
export interface ProjectSummary {
  slug: string
  name: string
  repo?: string
  task_counts: Record<string, number>
  doing_tasks?: TaskSummary[]
  trackers?: Tracker[]
}

/** service.CrossProjectContextResult - /api/context without a project. */
export interface CrossProjectContextResult {
  attention?: AttentionDigest
  focus_tasks?: TaskSummary[]
  note?: string
  projects: ProjectSummary[]
}

/** server.apiProject - one row of /api/projects (ListProjects + board metadata). */
export interface Project {
  slug: string
  name: string
  stack?: string
  /** Asleep: out of every group and every cockpit aggregation (project.yaml `archived: true`). */
  archived?: boolean
  /** Cockpit group slug - the project's own slug when it declares none. Never empty. */
  group: string
  /** Display name from config.yaml `cockpit.groups`; the slug when unnamed. */
  group_name: string
  task_counts: Record<string, number>
  path?: string
  repo?: string
  /** "Where we left off" (project.yaml notes; editable via POST /api/projects/{slug}). */
  notes?: string
  tags?: string[]
  links?: Record<string, string>
  /** The project's status list, in column order. */
  statuses: string[]
  landing_statuses: string[]
  /** project.yaml `slack` - the change feed's Slack mapping; absent = none. */
  slack?: SlackMapping
}

/** service.SlackMapping - a project's Slack workspace label + channels. */
export interface SlackMapping {
  workspace?: string
  channels: string[]
}

/** server.apiProjectsResult */
export interface ProjectsResult {
  projects: Project[]
  note?: string
}

/** storage.ProjectGroup - one cockpit group, derived from the active projects' `group` fields. */
export interface ProjectGroup {
  slug: string
  name: string
  /** Active member slugs, sorted; an archived project is in no group. */
  projects: string[]
}

/** service.ListGroupsResult - /api/groups */
export interface GroupsResult {
  groups: ProjectGroup[]
}

/** storage.RunCell - the RUN column of a run row. */
export interface RunCell {
  state?: string
  done: number
  total: number
}

/** storage.AcceptCell - the ACCEPTANCE column of a run row. */
export interface AcceptCell {
  state?: string
  host?: string
  age?: string
  visual_claims_open?: number
}

/** storage.RunRow - one line of `pm runs`. */
export interface RunRow {
  remote?: string
  project: string
  tracker?: string
  title?: string
  status?: string
  updated?: string
  run: RunCell
  acceptance: AcceptCell
  /** Raw RFC3339 stamps off the run-states (pm-cli-118-17); absent = no run / no acceptance. */
  run_started?: string
  run_updated?: string
  /** True while the manager is alive: run_updated is then a heartbeat, not an end. */
  run_live?: boolean
  accept_started?: string
  accept_updated?: string
  /** Set on a placeholder row for an unreachable remote runner. */
  note?: string
}

/** server.runsResult - /api/runs */
export interface RunsResult {
  rows: RunRow[]
}

/** server.focusResult - /api/focus */
export interface FocusResult {
  date?: string
  task_ids: string[]
  tasks: TaskSummary[]
}

/** storage.AttentionRow - one row of the attention queue, one shape for every section. */
export interface AttentionRow {
  section: string
  severity: 'crit' | 'warn' | 'info' | 'ok'
  project: string
  group: string
  task_id?: string
  /** The solo shift id on a solo_reports row. */
  shift?: string
  title: string
  status?: string
  /** One display-ready sentence: why the row is here. */
  reason: string
  /** A waiting row's raw reason - what an edit of it starts from. */
  waiting_for?: string
  /** null = unknown, render "since ?" - never derive from another stamp. */
  age_seconds: number | null
  since?: string
  flags?: string[]
  /** The closed action set the row may carry; the UI invents none. */
  actions: string[]
}

/** storage.AttentionSection */
export interface AttentionSection {
  name: string
  rows: AttentionRow[]
  /** Row count before any cap (changes: the feed's own count). */
  total: number
  note?: string
  /** Rows the user dismissed - not in rows, not in total. */
  dismissed?: number
}

/** service.DismissRow - what POST /api/attention/dismiss names a row by. */
export interface DismissRow {
  section: string
  project: string
  task_id?: string
  shift?: string
  since?: string
}

/** service.DismissResult */
export interface DismissResult {
  dismissed: number
}

/** service.RestoreResult */
export interface RestoreResult {
  restored: number
}

/** storage.ShiftTask */
export interface ShiftTask {
  id: string
  title: string
  /** The queue line's status - the task's status when the shift STARTED. */
  status?: string
  /** Off the task's Progress line: done | parked | untouched | doing. */
  outcome?: string
  branch?: string
}

/** storage.ShiftSummary - a shift at a glance, plain text. */
export interface ShiftSummary {
  title?: string
  done: number
  partial: number
  not_done: number
  parked: number
  untouched: number
  /** The TL;DR's sentence addressed to the user, cut to one line. */
  next?: string
}

/** storage.ReportTask - one task block of a solo report; markdown fields. */
export interface ReportTask {
  heading: string
  /** done | partial | not_done | untouched, empty when the report says none. */
  outcome?: string
  problem?: string
  state?: string
  checked?: string
  before_pr?: string
}

/** storage.ShiftReport - a solo report split into parts; markdown fields. */
export interface ShiftReport {
  title?: string
  next?: string
  summary?: string
  tasks: ReportTask[]
  decisions: string[]
  ideas: string[]
  cleanup?: string
  technical?: string
}

/** storage.Shift - one /solo shift off its state file. */
export interface Shift {
  project: string
  id: string
  kind: string
  date: string
  open: boolean
  closed?: string
  status_line: string
  tasks: ShiftTask[]
  file: string
  report?: string
  summary?: ShiftSummary
}

/** review.Review - one PR code review (GET /api/reviews, /api/reviews/{id}). */
export interface Review {
  id: string
  url: string
  /** owner/repo */
  repo: string
  number: number
  /** The PR title, from gh when the review started. */
  title?: string
  /** What the user pasted, when it was not the bare PR URL. */
  input?: string
  project: string
  /** The project's checkout; the review runs in `worktree` at `commit`. */
  dir: string
  worktree?: string
  commit?: string
  config_dir: string
  pid: number
  started: string
  state: 'running' | 'done' | 'error' | string
  finished?: string
  error?: string
  tokens?: { input: number; cache_creation: number; cache_read: number; output: number }
  /** The review markdown; only on GET /api/reviews/{id}. */
  report?: string
  /** A done review whose report says "No issues found". */
  no_issues?: boolean
  /** The Slack message the request came from, when it was a Slack link. */
  slack?: { workspace: string; channel: string; ts: string; thread_ts: string; server: string }
  /** When the PR was approved from the cockpit, or why the last try failed. */
  approved?: string
  approve_error?: string
  /** The ✅ on the Slack message after the approve. */
  slack_reacted?: string
  slack_react_error?: string
  /** The 👀 on the Slack message when the review started. */
  slack_seen?: string
  slack_seen_error?: string
}

/** GET /api/reviews */
export interface ReviewsResult {
  reviews: Review[]
}

/** solo.Input - the launch form of POST /api/solo and GET /api/solo/plan. */
export interface SoloInput {
  project: string
  /** A tracker id, task ids, a ticket key, a link or a sentence - handed to /solo verbatim. */
  queue: string
  /** '' (the skill decides) | 'sim' | 'web' | 'off'. */
  runtime?: string
  base?: string
  model?: string
  push?: boolean
  pr?: boolean
  max_tasks?: number
  max_hours?: number
}

/** solo.Plan - GET /api/solo/plan: the exact `claude --bg` launch, the dialog's preview. */
export interface SoloPlan {
  project: string
  input: SoloInput
  exe: string
  /** claude's arguments (claude itself excluded); the "/solo ..." prompt is last. */
  argv: string[]
  prompt: string
  cwd: string
  config_dir: string
  name: string
  warnings?: string[]
  ask_rules: number
}

/** solo.Launch - one background solo session pm started, with the supervisor's live state. */
export interface SoloLaunch {
  /** The short id `claude --bg` printed; attach/logs/stop take it. */
  id: string
  /** The session UUID (= the shift id), once the supervisor reported it. */
  session_id?: string
  project: string
  input: SoloInput
  argv: string[]
  cwd: string
  config_dir: string
  name: string
  started: string
  log: string
  error?: string
  stopped?: string
  /** starting | working | blocked | done | failed | stopped | unknown | error. */
  state: string
  /** busy | waiting | idle, while the process is alive. */
  status?: string
  waiting_for?: string
  pid?: number
  /** The commands to paste in a terminal. */
  attach: string
  logs: string
}

/** server.soloResult - GET /api/solo: the shifts the skill wrote + the launches pm started. */
export interface SoloResult {
  shifts: Shift[]
  launches: SoloLaunch[]
}

/** service.SoloReportResult - GET /api/solo/{project}/{shift}/report */
export interface SoloReportResult {
  shift: Shift
  kind: 'report' | 'state'
  markdown: string
  /** The report split into parts; only for kind "report". */
  digest?: ShiftReport
}

/** storage.GroupSummary - the sidebar's line for one group. */
export interface GroupSummary {
  slug: string
  name: string
  projects: string[]
  worst: 'crit' | 'warn' | 'info' | 'ok'
  failed: number
  visual: number
  waiting: number
  quiet: number
  last_activity?: string
}

/** storage.Attention - /api/attention[?project=|?group=] */
export interface Attention {
  generated: string
  /** Doing tasks touched this week - the header number. */
  wip: number
  sections: AttentionSection[]
  groups: GroupSummary[]
}

/** service.AttentionDigest - the `attention` block of /api/context. */
export interface AttentionDigest {
  wip: number
  needs_me: AttentionRow[]
  waiting: AttentionRow[]
  waiting_total: number
  stuck_projects: AttentionRow[]
  counts: Record<string, number>
  note?: string
}

/** feed.Event - one change; `seen` is applied by the reader from the seen mark. */
export interface ChangeEvent {
  id: string
  ts: string
  source: string
  project: string
  group: string
  task_id?: string
  title: string
  detail?: string
  url?: string
  severity: 'crit' | 'warn' | 'info' | 'ok'
  seen: boolean
}

/** feed.SourceStatus - one source's last run. */
export interface SourceStatus {
  name: string
  enabled: boolean
  last_fetch?: string
  error?: string
  events: number
}

/** feed.Changes - /api/changes: the cached events since the cutoff. */
export interface Changes {
  cutoff: string
  events: ChangeEvent[]
  unseen: number
  seen?: string
  sources: SourceStatus[]
}

/** feed.Result - POST /api/changes/refresh */
export interface RefreshResult {
  sources: SourceStatus[]
  added: number
  from: string
  to: string
}

/** server.seenResult - POST /api/changes/seen */
export interface SeenResult {
  seen: string
}

/** service.SidebarResult - cockpit.sidebar as the SPA reads it. */
export interface SidebarConfig {
  variant: 'columns' | 'plain' | 'rail' | string
  show_repos: boolean
  sort: 'worst' | 'last_activity' | 'manual' | string
  /** 0 = the SPA's own default. */
  width: number
}

/** service.ConfigGroup - one configured group, listed in the MANUAL sidebar order. */
export interface ConfigGroup {
  slug: string
  name: string
  /** 0 = unplaced (after every placed group). */
  order: number
}

/** service.CockpitResult - the resolved `cockpit:` block (defaults applied server-side). */
export interface CockpitConfig {
  groups: ConfigGroup[]
  doing_idle_days: number
  waiting_highlight_days: number
  stuck_project_days: number
  cutoff_hour: number
  refresh: { every_seconds: number; window: string }
  sections: Record<string, boolean>
  sources: Record<string, boolean>
  sidebar: SidebarConfig
  git: { all_branches: boolean }
  report: { model: string; language: string }
  /** cockpit.slack, without secrets: which workspaces have an MCP server (hand-edited in config.yaml). */
  slack: { workspaces: { workspace: string; source: string; has_me: boolean }[] }
  /** Executor runs on the cockpit (needs me, accepted no PR, in progress, the Runs table). */
  show_executor?: boolean
}

/** service.UpdateSettingsInput - POST /api/settings: a PATCH, absent = keep. */
export interface SettingsPatch {
  doing_idle_days?: number
  waiting_highlight_days?: number
  stuck_project_days?: number
  cutoff_hour?: number
  refresh?: { every_seconds?: number; window?: string }
  sections?: Record<string, boolean>
  sources?: Record<string, boolean>
  sidebar?: { variant?: string; show_repos?: boolean; sort?: string; width?: number }
  /** Merged by slug: name null keeps, '' clears; order 0 unplaces. */
  groups?: { slug: string; name?: string; order?: number }[]
  git?: { all_branches?: boolean }
  report?: { model?: string; language?: string }
  show_executor?: boolean
}

/** The fields POST /api/projects/{slug} accepts from the cockpit (service.UpdateProjectInput subset). */
export interface UpdateProjectBody {
  /** Empty = KEEP (the API treats empty notes as "leave"). */
  notes?: string
  /** '' = leave the group (the project becomes its own). */
  group?: string
  archived?: boolean
  /** An empty workspace with no channels removes the mapping. */
  slack?: SlackMapping
}

/** service.ConfigResult - GET /api/config */
export interface ConfigResult {
  cockpit: CockpitConfig
}

/** The fields POST /api/tasks/{project}/{id} accepts - a subset of service.UpdateTaskInput, tri-state like MCP: omit = keep, "" = clear. */
export interface UpdateTaskBody {
  status?: string
  waiting_for?: string
  brief?: string
  ac?: string
  body_append?: string
}

/** service.ToggleFocusResult - POST /api/focus/toggle */
export interface ToggleFocusResult {
  task_id: string
  focused: boolean
  date: string
  task_ids: string[]
}

/** service.ProjectResult - POST /api/projects/{slug} */
export interface ProjectResult {
  slug: string
  name: string
  notes?: string
  statuses?: string[]
  group?: string
  archived?: boolean
  slack?: SlackMapping
}

/** runctl.Flags - the launch toggles a run action takes (never sim). */
export interface RunFlags {
  yolo?: boolean
  additional?: boolean
}

/** runctl.Plan - GET /api/runs/{project}/{id}/plan: what an action would do. */
export interface RunPlan {
  action: string
  project: string
  task_id: string
  kind: string
  /** `pm` first; absent for claim / release_claim. */
  argv?: string[]
  cwd?: string
  log?: string
  warnings?: string[]
  additional_avail: boolean
  session?: string
  /** kill: "<kind> pid N (started ...)" or "nothing is running"; claim: the holder. */
  target?: string
  pid?: number
}

/** runctl.Started - a spawn's answer. */
export interface RunStarted {
  pid: number
  log: string
  argv: string[]
  kind: string
  warnings?: string[]
}

/** runctl.ClaimResult - claim / release_claim. */
export interface ClaimResult {
  claim?: {
    tracker_id: string
    host: string
    pid: number
    session?: string
    started: string
    refreshed: string
  }
  refreshed?: boolean
  released?: boolean
  heartbeat_seconds?: number
  max_hold_seconds?: number
}

/** runctl.KillResult */
export interface KillResult {
  kind: string
  pid: number
  parked?: string
  claim_released?: boolean
  note: string
}

export type RunActionResult = RunStarted | ClaimResult | KillResult

/** report.Suggestion - one proposed action; `action` empty = not mappable, `project` empty = unplaced. */
export interface ReportSuggestion {
  id: string
  project?: string
  task_id: string
  action?: string
  text: string
}

/** report.Report - one period's LLM report as stored. */
export interface Report {
  period: string
  cutoff: string
  generated: string
  model: string
  tokens?: { input: number; cache_creation: number; cache_read: number; output: number }
  duration_s: number
  text?: string
  suggestions: ReportSuggestion[]
  dismissed?: string[]
  error?: string
  events: number
  rows: number
}

/** server.reportState - GET /api/report (and POST's 202 answer). */
export interface ReportState {
  enabled: boolean
  period: string
  cutoff: string
  state: 'off' | 'none' | 'writing' | 'done' | 'error' | string
  model: string
  report?: Report
}
