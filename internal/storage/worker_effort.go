package storage

// WorkerEffort is what a DEAD worker's own transcript still says about how much
// work it did. It exists because the numbers pm normally reports - Turns and
// CostUSD on a sub - come off the `claude -p` result envelope, and a worker that
// dies never sends one. Both fields then stay 0, which in a retro is
// indistinguishable from a sub nobody ever started.
//
// Observed 2026-08-11: orbit-112-2 ran for 3749 s, pushed
// me-login/ACME-1501-calendar-dot-stale-after-create, died mid-fix with
// "claude worker failed: exit status 1" - and journaled turns 0, cost 0. Six of
// the 26 non-delivered subs in that retro window reported $0 the same way, so
// the "$139 spent on subs that did not land" figure the retro ranked its work by
// was a floor, not a number.
//
// It is a SEPARATE field rather than a fill-in of Turns/CostUSD on purpose. The
// envelope's numbers and these are not the same measurement, and quietly mixing
// them would make every later comparison lie:
//
//   - Turns here counts the main-loop input turns present in the transcript
//     (the initial prompt, each tool result, each harness injection). Checked
//     against the 14 subs whose envelope turns were recorded: identical on the 9
//     whose transcript holds a single harness injection, and HIGHER than the
//     envelope on the other 5 (e.g. pm-cli-72-2, envelope 3 vs 161 in the
//     transcript). So it is the same unit, read as an upper bound.
//   - There is no cost. A Claude Code transcript carries token usage but no
//     price, and deriving dollars would mean hardcoding a per-model rate card
//     that goes stale silently. OutputTokens is the honest substitute: the
//     model's own production, exactly summable, and a proxy a retro can rank by.
//
// OutputTokens counts the main loop only. A reviewer subagent's usage lives in
// its own sidechain transcript, so a sub whose review phase spawned three
// reviewers reports less than it truly spent - the number is a floor.
type WorkerEffort struct {
	// Source names where the numbers came from, so a consumer never has to
	// assume. "transcript" is the only value pm writes today.
	Source string `json:"source"`
	// Turns = main-loop input turns found in the transcript (upper bound of the
	// envelope's num_turns; see above).
	Turns int `json:"turns,omitempty"`
	// OutputTokens = sum of message.usage.output_tokens over the main-loop
	// assistant records. Floor: excludes subagent sidechains.
	OutputTokens int `json:"output_tokens,omitempty"`
}
