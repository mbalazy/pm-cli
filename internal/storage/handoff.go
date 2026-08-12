package storage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Handoff is the per-project ACCEPTANCE contract: the counterpart of
// the phase bindings, but for the pass that happens AFTER a run, by a human or
// a CC session rather than by pm.
//
// pm can now spawn the acceptance (`pm finish`, and `pm run-epic` chains it when
// the tracker says `finish_mode: auto`), but it still does not inject the
// PROCEDURE the way work_prompt.go does for a worker: the acceptance prompt
// tells a headless session to invoke the batch-finish skill, and the procedure
// stays in that skill. What pm CAN do is turn a file convention
// into a resolvable contract: the acceptance skill asks pm where the playbook and
// the runtime skill are instead of hardcoding a path.
//
// Deliberately minimal. Config states WHAT exists and WHERE it lives; the
// playbook states WHAT each tool is for and WHEN to reach for it. Process rules
// (escalation, "observe, don't reason") read identically in every project and
// belong in the global acceptance skill, not here - and anything pm can DERIVE
// (the runtime skill's scripts) is derived, never copied into config, since
// duplicated project knowledge drifting out of sync is the very problem this
// block exists to remove.
type Handoff struct {
	// Playbook is the project's evidence playbook, mapping the acceptance skill's
	// generic evidence categories to real commands here. Relative paths resolve
	// against the repo dir; "~" is expanded.
	Playbook string `yaml:"playbook,omitempty"`
	// RuntimeSkill names the project-local skill that drives the real runtime
	// (simulator, device, browser) - the one an acceptance needs in full, not by its
	// one-line description. Bare name, with or without a leading "/".
	RuntimeSkill string `yaml:"runtime_skill,omitempty"`
	// RigSkill names the skill that STANDS UP the runtime when it is down -
	// the cold-start procedure (local backend, dev server, booting the rig).
	// RuntimeSkill drives a runtime that exists; RigSkill brings one into
	// existence. Optional: a project whose runtime needs no standing up has
	// nothing to declare here. Unlike the runtime skill - which resolves in the
	// REPO ONLY, so that a misplaced one fails doctor before it fails a run on
	// another machine - the rig skill often lives in the user's global skills:
	// how to stand up THIS machine's dev loop is machine knowledge, not repo
	// knowledge, so resolution falls back to the project's Claude config dir
	// (claude_config_dir, default ~/.claude - the dir the acceptance session
	// actually loads skills from). Bare name, with or without a leading "/".
	RigSkill string `yaml:"rig_skill,omitempty"`
}

// IsZero reports whether the project declares no handoff contract at all.
func (h Handoff) IsZero() bool {
	return strings.TrimSpace(h.Playbook) == "" &&
		strings.TrimSpace(h.RuntimeSkill) == "" &&
		strings.TrimSpace(h.RigSkill) == ""
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
	RigSkill       string // normalised bare name; "" when not declared
	RigSkillPath   string // absolute path to the rig skill's SKILL.md / command .md; "" when not found
	// GlobalRoot is the Claude config dir the rig skill's fallback searched -
	// carried so doctor/show can name the actual location set they judged.
	GlobalRoot string
}

// ResolveHandoff resolves the executor's handoff block against the project's
// repo dir. claudeConfigDir is the project's pinned Claude config dir
// (Project.ResolveClaudeConfigDir()) - the ONE global root the rig skill may
// fall back to, because it is the dir the acceptance session actually loads
// skills from; empty degrades to the unpinned default. The runtime skill never
// uses it: it resolves in the repo only, so a runtime skill misplaced on one
// machine fails `pm executor doctor` here instead of failing the acceptance on
// the machine the project is synced to.
func (e Executor) ResolveHandoff(projPath, claudeConfigDir string) ResolvedHandoff {
	h := e.Handoff
	out := ResolvedHandoff{Declared: !h.IsZero()}
	if claudeConfigDir == "" {
		claudeConfigDir = DefaultClaudeConfigDir()
	}
	out.GlobalRoot = claudeConfigDir

	if p := strings.TrimSpace(h.Playbook); p != "" {
		out.PlaybookPath = resolveRepoPath(projPath, p)
		if st, err := os.Stat(out.PlaybookPath); err == nil && !st.IsDir() {
			out.PlaybookExists = true
		}
	}

	repoRoot := filepath.Join(projPath, ".claude")
	if name := strings.TrimPrefix(strings.TrimSpace(h.RuntimeSkill), "/"); name != "" {
		out.RuntimeSkill = name
		out.SkillPath, out.ScriptsDir, out.Scripts = resolveSkillRef([]string{repoRoot}, name)
	}
	if name := strings.TrimPrefix(strings.TrimSpace(h.RigSkill), "/"); name != "" {
		out.RigSkill = name
		// The rig skill's scripts are deliberately NOT surfaced: the runtime
		// skill's inventory exists because the playbook must explain those
		// tools, while a rig skill's SKILL.md is its own manual - read whole,
		// once, when the runtime is down.
		out.RigSkillPath, _, _ = resolveSkillRef([]string{repoRoot, claudeConfigDir}, name)
	}
	return out
}

// resolveSkillRef locates a named skill (or plain slash-command - it just
// cannot carry a scripts/ dir) under the given roots, first hit wins. The repo
// root always comes first, so a project-local skill beats a global one of the
// same name.
func resolveSkillRef(roots []string, name string) (skillPath, scriptsDir string, scripts []string) {
	for _, root := range roots {
		dir := filepath.Join(root, "skills", name)
		if md := filepath.Join(dir, "SKILL.md"); fileReadable(md) {
			d, s := skillScripts(dir)
			return md, d, s
		}
		if cmd := filepath.Join(root, "commands", name+".md"); fileReadable(cmd) {
			return cmd, "", nil
		}
	}
	return "", "", nil
}

// skillScripts returns the skill's scripts/ dir and the sorted names of the
// files in it. These are the concrete diagnostic tools an acceptance session should
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
