import {
  ArrowUpRight,
  BookOpen,
  Circle,
  CircleDot,
  Eye,
  EyeOff,
  FileText,
  GitPullRequest,
  Hourglass,
  Lock,
  LockOpen,
  Moon,
  Play,
  RotateCcw,
  Square,
  Undo2,
  type LucideProps,
} from 'lucide-react'

// One small icon per row action, drawn next to its text label (never in
// place of it - the label is the accessible name the tests and the user
// read). Unknown actions get no icon.

const ICONS: Record<string, React.ComponentType<LucideProps>> = {
  open: ArrowUpRight,
  report: FileText,
  claim: Lock,
  release_claim: LockOpen,
  rerun_finish: RotateCcw,
  resume_run: Play,
  kill: Square,
  focus_toggle: CircleDot,
  set_waiting_for: Hourglass,
  back_to_todo: Undo2,
  mark_seen: Eye,
  sleep_project: Moon,
  open_pr: GitPullRequest,
  focus: Circle,
  dismiss: EyeOff,
  open_report: BookOpen,
}

export function ActionIcon({ action, className }: { action: string; className?: string }) {
  const Icon = ICONS[action]
  if (!Icon) return null
  return <Icon aria-hidden="true" className={className ?? 'size-3'} strokeWidth={1.75} />
}
