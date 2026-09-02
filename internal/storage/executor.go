package storage

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Executor is the per-project execution profile that tells a worker HOW to
// implement/test/review/verify/pr in this project. It lives in an `executor`
// block in project.yaml and is consumed by `pm work` and `pm run-epic`.
//
// A project with NO executor block resolves to all-generic defaults (see
// defaultExecutor): every phase falls back to the engine's built-in generic,
// additional_worktree is off, statuses are todo/doing/merged (independent mode
// lands on "pushed" instead - see DoneStatusIndependent), fix_rounds is 2.
// The block is the ONLY project-specific piece of the engine.
type Executor struct {
	Enabled bool `yaml:"enabled"`
	// AdditionalWorktree = the LEGACY single "additional" worktree is CONFIGURED
	// for this project (makes worktree_path/env meaningful). It is a capability
	// gate, NOT "always use it": a run only uses a worktree when it opts in
	// per-run via --additional. DEFAULT false. Superseded by Worktrees - when
	// that list is set, this flag and WorktreePath are ignored.
	AdditionalWorktree bool `yaml:"additional_worktree"`
	// WorktreePath is where the LEGACY single "additional" worktree lives.
	// Relative paths resolve against the repo dir (`../foo-additional` -> a
	// sibling); "~" is expanded; empty defaults to "<repo>-additional". Only
	// consulted when AdditionalWorktree is true and Worktrees is empty.
	WorktreePath string `yaml:"worktree_path,omitempty"`
	// Worktrees is the multi-slot worktree pool: each slot is an isolated
	// worktree a run can claim via --additional (first free slot wins; --slot N
	// pins one). Per-slot Env overlays the executor-level Env (slot wins on
	// conflict) so shared keys live once at the top and per-slot keys (e.g. a
	// Metro port, a simulator UDID) differ per slot. When set, it supersedes the
	// legacy AdditionalWorktree/WorktreePath pair. Resolution rules are shared
	// with the legacy path (see ResolveWorktrees).
	Worktrees []WorktreeSlot `yaml:"worktrees,omitempty"`
	// BaseBranch is the fixed branch each fresh task/epic branch forks from in
	// additional-worktree mode, so a run never depends on whatever the user's main checkout
	// happens to have checked out. Precedence: explicit --base flag > this >
	// (pm work) the main checkout's current branch / (pm run-epic) "main".
	BaseBranch string `yaml:"base_branch,omitempty"`
	// Env is an opaque string->string map injected verbatim into spawned claude
	// processes: the executor worker when running with --additional, and the
	// board's interactive worktree launches (any executor.env, regardless of
	// AdditionalWorktree). pm does NOT interpret these - they carry
	// project-specific knowledge (e.g. a Metro port, a simulator UDID) that
	// stays out of pm and lives in project.yaml. Keys are arbitrary: name them
	// whatever the consuming project reads (e.g. SIM_UDID). Pairs are appended
	// LAST so they win over inherited values - which also means a
	// CLAUDE_CONFIG_DIR key here would override the config-dir pinning.
	Env map[string]string `yaml:"env,omitempty"`
	// WorkerClaudeConfigDir is the Claude config dir HEADLESS WORKERS run under
	// (`pm work`, `pm run-epic` subs) when it should differ from the project's
	// claude_config_dir. Everything a config dir holds rides in EVERY API call
	// of every worker and reviewer: retro pm-cli-119 (2026-09-02) measured
	// ~20k of a worker's 66k-token starting context as the user-scope skill
	// catalogue (47 skills, 57 KB of descriptions), the user CLAUDE.md and the
	// auto-memory index - none of which a worker (no MCP, repo skills only)
	// uses, re-read on each of its ~150 calls. A slim dir with none of them
	// cuts that on every call; Claude Code has no flag for it
	// (--setting-sources gates settings.json, not CLAUDE.md or skills).
	// `pm finish` keeps the project's claude_config_dir ON PURPOSE: the
	// acceptance skills live in the user scope this dir exists to leave out.
	// The dir must be logged in once by hand (`CLAUDE_CONFIG_DIR=<dir> claude`);
	// `pm executor doctor` checks it exists. "~" is expanded; empty = the same
	// dir as claude_config_dir.
	WorkerClaudeConfigDir string `yaml:"worker_claude_config_dir,omitempty"`
	// SeedExclude overrides DefaultSeedExcludes for worktree seeding
	// (CopyUntrackedFiles): untracked directories that must NOT be copied into a
	// fresh worktree (dependency/build artifacts). When set it REPLACES the
	// defaults, so include the defaults you still want.
	SeedExclude []string `yaml:"seed_exclude,omitempty"`
	// Prepare is an optional shell command run in the CLAIMED worktree slot
	// (cwd = the worktree) after branch setup, e.g. `yarn install
	// --frozen-lockfile` - it makes dependency freshness structural instead of
	// relying on the worker to discover missing modules via a red verify. Runs
	// ONCE per run: the epic manager runs it once for the whole epic (not per
	// sub); `pm work` runs it once before its worker. Only in --additional mode
	// (the main checkout's deps are the user's business). A failure aborts the
	// run. pm does not interpret the command.
	Prepare string `yaml:"prepare,omitempty"`
	// Baseline is an optional shell command (typically the project's full
	// verification, e.g. `yarn validate`) whose output is captured ONCE per run
	// on the branch the work forks from, BEFORE any worker starts, and injected
	// into every worker prompt as the set of PRE-EXISTING failures. Without it,
	// a worker landing in a repo whose verification is already red cannot prove
	// which failures are its own - in practice it burns turns rediscovering old
	// breakage and then reports a false failure. With a baseline the verdict is
	// judged on NEW failures only. The command's exit code is expected to be
	// non-zero on a red baseline - that is data, not an error; a baseline that
	// fails to even start degrades to "no baseline" with a warning. pm does not
	// interpret the command or its output.
	Baseline string `yaml:"baseline,omitempty"`
	// ContextRepos maps a short name to a local repo path the worker may READ
	// for cross-repo context (e.g. backend: ../platform.orbit to check an
	// endpoint's real shape). Listed in the worker prompt as read-only reference
	// repos; pm itself never touches them.
	ContextRepos map[string]string `yaml:"context_repos,omitempty"`
	// Handoff is the ACCEPTANCE contract: where the evidence playbook
	// and the runtime-driving skill live. Unlike every other field here it is
	// consumed AFTER a run - by a human, a CC session (`pm executor show`), or
	// the acceptance worker `pm finish` spawns - rather than by a worker
	// prompt. See handoff.go.
	Handoff     Handoff `yaml:"handoff,omitempty"`
	StartStatus string  `yaml:"start_status,omitempty"` // sub status meaning "ready to pick up"
	WipStatus   string  `yaml:"wip_status,omitempty"`
	DoneStatus  string  `yaml:"done_status,omitempty"` // where a verified sub lands in INTEGRATION mode
	// DoneStatusIndependent is where a verify-green sub lands in INDEPENDENT
	// (batch) mode, where the manager pushes the sub's own branch and merges
	// NOTHING. Reusing DoneStatus there made every batch sub land on "merged"
	// while its journal note in the same breath read "pushed <branch>; TODO:
	// verify on the simulator" - the status claimed work was integrated when it
	// was sitting on origin waiting for a human. Empty = DefaultIndependentDoneStatus.
	DoneStatusIndependent string                  `yaml:"done_status_independent,omitempty"`
	FixRounds             int                     `yaml:"fix_rounds,omitempty"` // review->fix loop cap before escalating
	Phases                map[string]PhaseBinding `yaml:"phases,omitempty"`
	Notes                 string                  `yaml:"notes,omitempty"`
	// Timeout is the wall-clock ceiling for ONE worker in this project, as a
	// duration string ("90m"). Empty/zero = the command's own default (120m); an
	// explicit --timeout on the run still wins over both.
	//
	// It exists because the ceiling is a property of the PROJECT (how long a real
	// sub takes on this machine, in this repo, with this test suite) while the
	// only place to say it was a flag - so a slower machine needed the flag
	// remembered on every single invocation, and the runs that hit the wall show
	// what forgetting costs: pm-cli-57, pm-cli-72-1 and orbit-1-2 all died
	// at ~3600 s with their branch ALREADY pushed, i.e. real work truncated, not a
	// hung worker. In the same window five green subs finished between 2839 s and
	// 3530 s - the wall sits inside the distribution, not outside it.
	Timeout Duration `yaml:"timeout,omitempty"`
	// ReviewModel is the model reviewer subagents run on, independent of the
	// run's own --model. Reviewers were the single largest line item measured in
	// epic orbit-106 (58% of the epic's tokens) and they inherited opus
	// purely because nobody named a model for them. Empty = DefaultReviewModel;
	// the literal "inherit" turns the pinning off and restores pre-0.38
	// behaviour, which is also the rollback for this one change alone.
	ReviewModel string `yaml:"review_model,omitempty"`
}

// DefaultReviewModel is what reviewer subagents run on when a project does not
// say otherwise.
//
// This was "sonnet" for exactly as long as it took to measure it. Gate G2
// (pm-cli-96-6, 2026-08-07) replayed two review-forced findings out of epic
// orbit-106 through the new harness, each as a single reviewer holding
// the diff packet with no Bash - once on sonnet, once on opus, everything else
// identical:
//
//   - the four stage-advance sites firing two concurrent PATCHes to one JSONB
//     field, where a failed one's rollback pushes a stale stage back;
//   - the persisted onboarding screen having no forward ratchet, so one
//     back-then-continue moves a provider's resume point permanently backwards.
//
// Opus reported both. Sonnet reported neither - and in both runs it named the
// exact mechanism and then argued it away ("I traced the one plausible risk I
// considered ... down to RTK Query's synchronous optimistic-dispatch behavior,
// which rules it out"). A reviewer that misses a bug costs one bug; a reviewer
// that examines it and clears it costs the bug AND the belief that it was
// looked at.
//
// So the model is where the money does NOT get saved. The savings this ticket
// keeps are structural and untouched by this line: pm sizes the reviewer count
// from the diff (1-2, not 4-6), the round cap is 2 and a round only exists if
// the last one found something (not a flat 3), and each reviewer is handed the
// diff instead of pulling ~142kB out of the repo. Fewer, better-fed reviewers
// on the better model.
//
// Pinning rather than inheriting is deliberate now that the two are separable:
// review quality should not follow a run onto a cheap model chosen for a
// trivial sub. A project that wants the old coupling sets review_model:
// inherit.
//
// Re-run the gate before changing this: the method and its cost ($9.10, four
// reviewer runs) are recorded in pm-cli-96-6.
const DefaultReviewModel = "opus"

// ReviewModelInherit is the opt-out spelling. It reads as what it does at the
// call site ("let the reviewer inherit the run's model") where an empty string
// would be indistinguishable from "unset, so use the default".
const ReviewModelInherit = "inherit"

// ResolveReviewModel returns the model to pin onto reviewer subagents, or ""
// when pm must not touch the model at all.
func (e Executor) ResolveReviewModel() string {
	switch strings.TrimSpace(e.ReviewModel) {
	case "":
		return DefaultReviewModel
	case ReviewModelInherit:
		return ""
	default:
		return strings.TrimSpace(e.ReviewModel)
	}
}

// DefaultIndependentDoneStatus is the landing status for a verify-green sub in
// independent (batch) mode when the project does not name one. It says what the
// manager actually did - it pushed the branch - and deliberately differs from
// the integration-mode default ("merged"), which says what happens there.
const DefaultIndependentDoneStatus = "pushed"

// IndependentDoneStatus returns the landing status for independent (batch) mode
// plus whether the project named it explicitly. The flag matters at the gate:
// an explicitly configured status that the project's `statuses` list does not
// contain is a config error worth failing on, whereas the built-in default
// falling outside an older project's status list is not - that one degrades to
// DoneStatus with a warning, so upgrading pm never breaks a batch mid-flight.
func (e Executor) IndependentDoneStatus() (status string, explicit bool) {
	if s := strings.TrimSpace(e.DoneStatusIndependent); s != "" {
		return s, true
	}
	return DefaultIndependentDoneStatus, false
}

// LandingStatuses returns every status this project's executor moves a
// verify-green sub to: the integration one and the independent one. This is the
// answer to "is this task finished as far as the executor is concerned" -
// consumers must ask for it instead of testing against the literal "merged",
// which is only the DEFAULT of one of the two knobs (pm-cli-82).
func (e Executor) LandingStatuses() []TaskStatus {
	indep, _ := e.IndependentDoneStatus()
	var out []TaskStatus
	for _, s := range []string{e.DoneStatus, indep} {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		st := TaskStatus(s)
		if !slices.Contains(out, st) {
			out = append(out, st)
		}
	}
	return out
}

// WorktreeSlot is one entry of the executor's worktree pool: where the
// worktree lives (same path rules as the legacy worktree_path) plus the
// per-slot env overrides layered on top of the executor-level Env.
type WorktreeSlot struct {
	Path string            `yaml:"path,omitempty"`
	Env  map[string]string `yaml:"env,omitempty"`
}

// The fixed, universal set of phases. The engine knows phase SEMANTICS; the
// project supplies the BINDINGS for each.
const (
	PhaseImplement = "implement"
	PhaseTest      = "test"
	PhaseReview    = "review"
	PhaseVerify    = "verify"
	PhasePR        = "pr"
)

// ExecutorPhases is the canonical phase ordering for the inner loop.
var ExecutorPhases = []string{PhaseImplement, PhaseTest, PhaseReview, PhaseVerify, PhasePR}

// BindKind is the resolved state of a single phase binding.
type BindKind int

const (
	BindGeneric BindKind = iota // empty/absent -> engine's built-in generic (NOT skip)
	BindSkill                   // skill: "/foo" -> run the project's custom skill
	BindCmd                     // cmd: "..."   -> run raw shell
	BindSkip                    // false        -> skip this phase entirely
)

func (k BindKind) String() string {
	switch k {
	case BindSkill:
		return "skill"
	case BindCmd:
		return "cmd"
	case BindSkip:
		return "skip"
	default:
		return "generic"
	}
}

// PhaseBinding is a 4-state union for how a single phase runs:
//
//	skill: "/foo"   -> BindSkill   (project's custom skill)
//	cmd:   "make x" -> BindCmd     (raw shell)
//	(empty/absent)  -> BindGeneric (engine's built-in generic fallback)
//	false           -> BindSkip    (omit the phase)
//
// In YAML a binding is either a scalar bool (`false`) or a mapping
// (`{ skill: ... }` / `{ cmd: ... }`). An empty/missing value is generic.
type PhaseBinding struct {
	Skill string `yaml:"skill,omitempty"`
	Cmd   string `yaml:"cmd,omitempty"`
	Skip  bool   `yaml:"-"`
}

// Kind resolves the binding to exactly one of skill|cmd|generic|skip.
// Precedence when over-specified: skip > skill > cmd > generic.
func (b PhaseBinding) Kind() BindKind {
	switch {
	case b.Skip:
		return BindSkip
	case b.Skill != "":
		return BindSkill
	case b.Cmd != "":
		return BindCmd
	default:
		return BindGeneric
	}
}

// MarshalYAML is the round-trip counterpart of UnmarshalYAML: a skip binding
// renders back as the scalar `false`, everything else as a mapping. Without
// this, Skip's `yaml:"-"` tag makes a plain struct marshal emit `{}` for a
// skip binding - indistinguishable from generic - so ANY writeProject rewrite
// (e.g. an unrelated notes-only pm_update_project call) silently flips a
// `pr: false` binding back to generic on the next read (pm-cli-55).
func (b PhaseBinding) MarshalYAML() (any, error) {
	if b.Skip {
		return false, nil
	}
	return struct {
		Skill string `yaml:"skill,omitempty"`
		Cmd   string `yaml:"cmd,omitempty"`
	}{Skill: b.Skill, Cmd: b.Cmd}, nil
}

// UnmarshalYAML accepts either a scalar bool (false=skip, true=generic) or a
// mapping with skill/cmd keys.
func (b *PhaseBinding) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return fmt.Errorf("phase binding scalar must be a bool (false=skip), got %q", node.Value)
		}
		// false -> skip; true -> explicit generic (no skill/cmd).
		b.Skip = !enabled
		return nil
	case yaml.MappingNode:
		var aux struct {
			Skill string `yaml:"skill"`
			Cmd   string `yaml:"cmd"`
		}
		if err := node.Decode(&aux); err != nil {
			return err
		}
		b.Skill = aux.Skill
		b.Cmd = aux.Cmd
		return nil
	default:
		return fmt.Errorf("phase binding must be a bool or a {skill|cmd} mapping")
	}
}

// defaultExecutor returns the all-generic profile applied when a project has no
// executor block, and the baseline that an explicit block is overlaid onto.
func defaultExecutor() Executor {
	return Executor{
		Enabled:            true,
		AdditionalWorktree: false,
		StartStatus:        "todo",
		WipStatus:          "doing",
		DoneStatus:         "merged",
		FixRounds:          2,
	}
}

// UnmarshalYAML overlays the YAML block onto defaultExecutor so that omitted
// fields keep their defaults (e.g. fix_rounds stays 3, additional_worktree
// stays false) while present fields override. Phases stay as parsed;
// missing phases resolve to generic via Phase().
func (e *Executor) UnmarshalYAML(node *yaml.Node) error {
	type rawExecutor Executor
	raw := rawExecutor(defaultExecutor())
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*e = Executor(raw)
	return nil
}

// Phase returns the binding for a named phase. A missing/empty binding resolves
// to the generic fallback (zero PhaseBinding, Kind() == BindGeneric).
func (e Executor) Phase(name string) PhaseBinding {
	if e.Phases == nil {
		return PhaseBinding{}
	}
	return e.Phases[name]
}

// EnvSlice renders Env as a sorted []string of KEY=VALUE pairs for injection
// into a spawned process's environment. Sorted purely for determinism; nil when
// no env is configured. pm does not interpret these values.
func (e Executor) EnvSlice() []string {
	if len(e.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e.Env))
	for k := range e.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+e.Env[k])
	}
	return out
}

// GetExecutor returns the resolved execution profile for a project: an explicit
// block (already defaulted via UnmarshalYAML) or the all-generic defaults when
// no block is present.
func (p *Project) GetExecutor() Executor {
	if p.Executor == nil {
		return defaultExecutor()
	}
	return *p.Executor
}

// HasExecutor reports whether the project declares an explicit executor block.
func (p *Project) HasExecutor() bool {
	return p.Executor != nil
}
