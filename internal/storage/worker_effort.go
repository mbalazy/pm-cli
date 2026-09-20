package storage

// WorkerEffort is what a DEAD worker's own transcript still says about how much
// work it did. It exists because the numbers pm normally reports - Turns and
// CostUSD on a sub - come off the `claude -p` result envelope, and a worker that
// dies never sends one. Both fields then stay 0, which in a retro is
// indistinguishable from a sub nobody ever started.
//
// Observed 2026-08-11: one sub ran for 3749 s, pushed its working branch,
// died mid-fix with
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
// TokenUsage is what one worker run cost in TOKENS, read off the claude
// envelope's `usage` object (Claude Code 2.1.258: input_tokens,
// cache_creation_input_tokens, cache_read_input_tokens, output_tokens).
//
// It exists because the number a subscription's 5-hour window meters is
// tokens, not dollars, and retro 2026-09-02 (pm-cli-119) had to reconstruct it
// by summing usage records across every worker and reviewer transcript: run
// pm-cli-118 read ~49M input tokens in 64 minutes, 95% of them cache reads of
// a 100-190k context re-sent on each of ~470 API calls - a shape `turns` and
// `cost_usd` cannot show (the run cost $23). Input is split three ways for
// the same reason: cache reads are the bulk and are billed at a fraction of
// the uncached price, so their SHARE is the number that says whether a run
// was expensive or merely long.
//
// Whether the envelope's usage includes subagent (reviewer) sidechains is not
// documented; the first real run compares this against a transcript sum.
type TokenUsage struct {
	Input         int `json:"input"`
	CacheCreation int `json:"cache_creation"`
	CacheRead     int `json:"cache_read"`
	Output        int `json:"output"`
}

// TotalInput is every token the model READ across the run: uncached input
// plus both cache buckets. This is the figure a rate limit sees.
func (u TokenUsage) TotalInput() int { return u.Input + u.CacheCreation + u.CacheRead }

// CacheReadShare is the fraction of TotalInput served from the prompt cache
// (0 when nothing was read).
func (u TokenUsage) CacheReadShare() float64 {
	if t := u.TotalInput(); t > 0 {
		return float64(u.CacheRead) / float64(t)
	}
	return 0
}

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
