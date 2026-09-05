// The LLM report over the feed (pm-cli-118-20). This build has no report
// endpoint yet: the panel holds the place above the feed and says why it
// is empty, so the screen's shape is final before the content lands.

interface Props {
  /** True when cockpit.sources.report is on (the config's toggle). */
  enabled: boolean
}

export function ReportPanel({ enabled }: Props) {
  return (
    <section aria-label="Report" className="rounded border border-dashed p-3 text-sm">
      <h2 className="mb-1 flex flex-wrap items-baseline gap-2">
        <span className="font-semibold">Report</span>
        <span className="text-xs text-gray-400">
          written by an LLM from the raw feed below, one per period · optional, costs tokens
        </span>
      </h2>
      <p className="text-gray-500">
        {enabled
          ? 'report is switched on, but this build cannot write one yet (pm-cli-118-20)'
          : 'report is off (Settings › Sources › report)'}
      </p>
    </section>
  )
}
