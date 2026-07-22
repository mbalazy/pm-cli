package storage

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Executor is the per-project execution profile that tells a worker HOW to
// implement/test/review/verify/pr in this project. It lives in an `executor`
// block in project.yaml and is consumed by `pm work` and `pm run-epic`.
//
// A project with NO executor block resolves to all-generic defaults (see
// defaultExecutor): every phase falls back to the engine's built-in generic,
// additional_worktree is off, statuses are todo/doing/merged, fix_rounds is 3, gates are
// human. The block is the ONLY project-specific piece of the engine.
type Executor struct {
	Enabled bool `yaml:"enabled"`
	// AdditionalWorktree = the isolated "additional" worktree is CONFIGURED for
	// this project (makes worktree_path/base_branch/env meaningful). It is a
	// capability gate, NOT "always use it": a run only uses the worktree when it
	// opts in per-run via --additional. DEFAULT false.
	AdditionalWorktree bool `yaml:"additional_worktree"`
	// WorktreePath is where the single "additional" worktree lives. Relative
	// paths resolve against the repo dir (`../foo-additional` -> a sibling); "~"
	// is expanded; empty defaults to "<repo>-additional". Only consulted when
	// AdditionalWorktree is true. There is exactly ONE such worktree (not N slots).
	WorktreePath string `yaml:"worktree_path,omitempty"`
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
	Env         map[string]string       `yaml:"env,omitempty"`
	StartStatus string                  `yaml:"start_status,omitempty"` // sub status meaning "ready to pick up"
	WipStatus   string                  `yaml:"wip_status,omitempty"`
	DoneStatus  string                  `yaml:"done_status,omitempty"` // where a verified sub lands
	FixRounds   int                     `yaml:"fix_rounds,omitempty"`  // review->fix loop cap before escalating
	Gate        Gate                    `yaml:"gate,omitempty"`
	Phases      map[string]PhaseBinding `yaml:"phases,omitempty"`
	Notes       string                  `yaml:"notes,omitempty"`
}

// GateMode controls whether a side-effecting step (pr, merge) is human-gated or
// allowed to run automatically.
type GateMode string

const (
	GateHuman GateMode = "human"
	GateAuto  GateMode = "auto"
)

// Gate holds the per-action autonomy gates. Client repos keep these human.
type Gate struct {
	PR    GateMode `yaml:"pr,omitempty"`
	Merge GateMode `yaml:"merge,omitempty"`
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
		FixRounds:          3,
		Gate:               Gate{PR: GateHuman, Merge: GateHuman},
	}
}

// UnmarshalYAML overlays the YAML block onto defaultExecutor so that omitted
// fields keep their defaults (e.g. fix_rounds stays 3, gates stay human,
// additional_worktree stays false) while present fields override. Phases stay as parsed;
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
