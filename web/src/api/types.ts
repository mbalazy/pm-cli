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
  focus_tasks?: TaskSummary[]
  note?: string
  projects: ProjectSummary[]
}

/** server.apiProject - one row of /api/projects (ListProjects + board metadata). */
export interface Project {
  slug: string
  name: string
  stack?: string
  archived?: boolean
  task_counts: Record<string, number>
  path?: string
  repo?: string
  tags?: string[]
  links?: Record<string, string>
  /** The project's status list, in column order. */
  statuses: string[]
  landing_statuses: string[]
}

/** server.apiProjectsResult */
export interface ProjectsResult {
  projects: Project[]
  note?: string
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
