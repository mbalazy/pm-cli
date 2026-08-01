package storage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Handoff is the per-project ODBIÓR (acceptance) contract: the counterpart of
// the phase bindings, but for the pass that happens AFTER a run, by a human or
// a CC session rather than by pm.
//
// Because pm never spawns the odbiór, it cannot inject a prompt the way
// work_prompt.go does for a worker. What it CAN do is turn a file convention
// into a resolvable contract: the odbiór skill asks pm where the playbook and
// the runtime skill are instead of hardcoding a path.
//
// Deliberately minimal. Config states WHAT exists and WHERE it lives; the
// playbook states WHAT each tool is for and WHEN to reach for it. Process rules
// (escalation, "observe, don't reason") read identically in every project and
// belong in the global odbiór skill, not here - and anything pm can DERIVE
// (the runtime skill's scripts) is derived, never copied into config, since
// duplicated project knowledge drifting out of sync is the very problem this
// block exists to remove.
type Handoff struct {
	// Playbook is the project's evidence playbook, mapping the odbiór skill's
	// generic evidence categories to real commands here. Relative paths resolve
	// against the repo dir; "~" is expanded.
	Playbook string `yaml:"playbook,omitempty"`
	// RuntimeSkill names the project-local skill that drives the real runtime
	// (simulator, device, browser) - the one an odbiór needs in full, not by its
	// one-line description. Bare name, with or without a leading "/".
	RuntimeSkill string `yaml:"runtime_skill,omitempty"`
}

// IsZero reports whether the project declares no handoff contract at all.
func (h Handoff) IsZero() bool {
	return strings.TrimSpace(h.Playbook) == "" && strings.TrimSpace(h.RuntimeSkill) == ""
}

// ResolvedHandoff is the handoff contract resolved against the repo on disk:
// absolute paths, existence facts, and the script inventory DERIVED from the
// runtime skill (never configured). Every field is best-effort - a missing file
// is reported, never an error, so `pm executor show` prints a profile even for
// a half-configured project and `pm executor doctor` is the one that judges it.
type ResolvedHandoff struct {
	Declared       bool
	PlaybookPath   string // absolute; "" when not declared
	PlaybookExists bool
	RuntimeSkill   string // normalised bare name; "" when not declared
	SkillPath      string // absolute path to the skill's SKILL.md / command .md; "" when not found
	ScriptsDir     string // absolute; "" when the skill has no scripts/ dir
	Scripts        []string
}

// ResolveHandoff resolves the executor's handoff block against the project's
// repo dir.
func (e Executor) ResolveHandoff(projPath string) ResolvedHandoff {
	h := e.Handoff
	out := ResolvedHandoff{Declared: !h.IsZero()}

	if p := strings.TrimSpace(h.Playbook); p != "" {
		out.PlaybookPath = resolveRepoPath(projPath, p)
		if st, err := os.Stat(out.PlaybookPath); err == nil && !st.IsDir() {
			out.PlaybookExists = true
		}
	}

	name := strings.TrimPrefix(strings.TrimSpace(h.RuntimeSkill), "/")
	if name == "" {
		return out
	}
	out.RuntimeSkill = name

	skillDir := filepath.Join(projPath, ".claude", "skills", name)
	if md := filepath.Join(skillDir, "SKILL.md"); fileReadable(md) {
		out.SkillPath = md
		out.ScriptsDir, out.Scripts = skillScripts(skillDir)
		return out
	}
	// A runtime helper may also live as a plain slash-command; it just cannot
	// carry a scripts/ dir.
	if cmd := filepath.Join(projPath, ".claude", "commands", name+".md"); fileReadable(cmd) {
		out.SkillPath = cmd
	}
	return out
}

// skillScripts returns the skill's scripts/ dir and the sorted names of the
// files in it. These are the concrete diagnostic tools an odbiór session should
// know exist - pm derives them so no config or playbook has to list them.
func skillScripts(skillDir string) (string, []string) {
	dir := filepath.Join(skillDir, "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", nil
	}
	return dir, names
}

// resolveRepoPath resolves a config path against the repo dir with the same
// rules as ResolveWorktreeDir, minus its empty-value default.
func resolveRepoPath(projPath, p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		return expandTilde(p)
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(projPath, p))
}

func fileReadable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
