package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// defaultPlaybookPath is where `pm executor init` proposes the evidence
// playbook. Relative, so it resolves against the repo and travels with it.
const defaultPlaybookPath = ".claude/evidence-playbook.md"

// playbookTODO marks a slot the generator could not fill. `pm executor doctor`
// warns while any of these survive - without that, a scaffold whose prose is
// still empty would silence the very check it was meant to satisfy, because
// checkScriptsMentioned only asks whether a script is NAMED anywhere.
const playbookTODO = "TODO:"

// playbookPlan is what init intends to do about the playbook file. Split from
// the writing so `--dry-run` can report it without touching the disk.
type playbookPlan struct {
	Path    string // absolute
	Content string // empty when nothing should be written
	Exists  bool
}

// planPlaybook decides whether to scaffold the playbook the profile declares.
// An existing file is never rewritten - it is hand-written prose, and the only
// safe operation on it is leaving it alone.
func planPlaybook(projPath, slug string, e *storage.Executor) *playbookPlan {
	if strings.TrimSpace(e.Handoff.Playbook) == "" {
		return nil
	}
	h := e.ResolveHandoff(projPath)
	if h.PlaybookPath == "" {
		return nil
	}
	plan := &playbookPlan{Path: h.PlaybookPath, Exists: h.PlaybookExists}
	if !plan.Exists {
		plan.Content = playbookSkeleton(slug, e, h)
	}
	return plan
}

// write creates the playbook, refusing to clobber a file that appeared in the
// meantime.
func (p *playbookPlan) write() error {
	if p == nil || p.Content == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(p.Content)
	return err
}

// playbookSkeleton renders the scaffold.
//
// The shape is the whole point, and it is the lesson from pm-cli-49: a LIST OF
// SCRIPT NAMES IS NOT DOCUMENTATION. pm can derive what exists and where it
// lives; only a human can say what a tool is FOR and when to reach for it. So
// every derived fact is filled in, and every judgement is left as a labelled
// empty slot rather than a plausible-looking guess - an invented purpose is
// worse than a blank, because nobody goes back to check it.
func playbookSkeleton(slug string, e *storage.Executor, h storage.ResolvedHandoff) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Evidence playbook - %s\n\n", slug)
	b.WriteString(`Scaffolded by ` + "`pm executor init`" + `. It maps the generic evidence categories an
odbiór (acceptance) session works with onto the real commands of THIS project.
The generic process lives in the global odbiór skill; this file only says which
tools realize each category here, and when to reach for them.

Read the companion facts with ` + "`pm executor show " + slug + "`" + ` - it prints the worktree
slots with their env (device ids, dev-server ports), the context repos and the
resolved handoff paths. Never copy those values into this file: pm resolves them
from config, and a copy here is wrong the moment a slot changes.

Every ` + playbookTODO + ` below is a slot pm could not fill because the answer is a
judgement, not a fact on disk. ` + "`pm executor doctor " + slug + "`" + ` keeps warning while
any of them survive.

`)

	b.WriteString("## Evidence tools\n\n")
	b.WriteString("What settles a claim in this project, per category. Evidence means observed\noutput - a log line, a dump, a source file - never reasoning about what the\ncode probably does.\n\n")
	b.WriteString("- **Runtime logs:** " + playbookTODO + " which command reads this app's own log output, and\n  what its limits are (does it work on a physical device? since when does it\n  capture?).\n")
	b.WriteString("- **Network traffic:** " + playbookTODO + " how to see the real HTTP a flow produces, and\n  whether it is available outside dev builds.\n")

	if len(e.ContextRepos) > 0 {
		b.WriteString("- **Authoritative source (read-only reference repos):**\n")
		for _, name := range sortedKeys(e.ContextRepos) {
			fmt.Fprintf(&b, "  - `%s` -> `%s`\n", name, e.ContextRepos[name])
			fmt.Fprintf(&b, "    %s what question this repo settles, and where to look in it (which\n    packages hold the contracts / enums / routes worth grepping).\n", playbookTODO)
		}
		b.WriteString("\n  Grep these before trusting any inference made on this side of the wire.\n")
	} else {
		b.WriteString("- **Authoritative source:** " + playbookTODO + " where the other side of a contract lives\n  (backend repo, schema, API docs) and how to consult it. If it is a local\n  checkout, add it to `executor.context_repos` so workers get told about it too.\n")
	}
	b.WriteString("- **Direct API call:** " + playbookTODO + " how to call the dev API by hand (base URL, how to\n  get a token, and any rule about how the command must be shaped).\n\n")

	b.WriteString("## Runtime verification\n\n")
	if h.RuntimeSkill != "" {
		fmt.Fprintf(&b, "Skill: `/%s`", h.RuntimeSkill)
		if h.SkillPath != "" {
			fmt.Fprintf(&b, " -> `%s`", h.SkillPath)
		}
		b.WriteString("\n\n**Read that SKILL.md in full**, not its one-line description - the how-to\n(flags, worked examples, failure modes) is in its own sections and does not get\nsummarized here.\n\n")
	} else {
		b.WriteString(playbookTODO + " no runtime skill is declared. Name the skill that drives the real\nruntime (simulator, device, browser) and set `executor.handoff.runtime_skill`\nto it, so an odbiór session can ask pm instead of guessing.\n\n")
	}
	b.WriteString("- **Which runtime to trust:** " + playbookTODO + " the primary one, and how to tell what\n  state it is in (which branch is it serving? who owns its port?).\n")
	b.WriteString("- **Secondary runtime:** the worktree slots. Get each slot's device id and\n  port from `pm executor show " + slug + "`.\n")
	b.WriteString("- **Gotchas that fake a pass or a fail:** " + playbookTODO + " persisted/cached state that\n  survives a rebuild, flows that only work on a physical device, anything that\n  crashes the simulator.\n\n")

	if len(h.Scripts) > 0 {
		b.WriteString("## Diagnostic tools - what each one is FOR\n\n")
		fmt.Fprintf(&b, "`pm executor show %s` lists these scripts and where they live. It cannot say\nwhat they are for; that is this section's job.\n\n", slug)
		b.WriteString("**Escalation rule: one round of looking that does not settle a claim becomes\ninstrumentation, not a second round of looking.** A screenshot cannot separate\na wrong position from wrong arithmetic, and an accessibility tree gives a\nview's frame, not where the content sits inside it.\n\n")
		for _, s := range h.Scripts {
			fmt.Fprintf(&b, "- **`%s`**\n", s)
			fmt.Fprintf(&b, "  - %s what it is FOR - the one kind of question it answers.\n", playbookTODO)
			fmt.Fprintf(&b, "  - %s WHEN to reach for it - the symptom that should make you run it\n    instead of looking again.\n", playbookTODO)
			fmt.Fprintf(&b, "  - %s how to read its output - what counts as a pass, what counts as a\n    real defect.\n", playbookTODO)
		}
		b.WriteString("\n**Where to instrument:** " + playbookTODO + " and remember every overlay, log line and\nhardcoded prop is throwaway - remove it and confirm the tree is clean before\nthe PR.\n\n")
	} else if h.RuntimeSkill != "" {
		b.WriteString("## Diagnostic tools - what each one is FOR\n\n")
		fmt.Fprintf(&b, "`/%s` ships no `scripts/` directory, so there is nothing for pm to\nderive here. %s if the project has diagnostic tooling elsewhere, list it: what\neach tool is FOR, when to reach for it, how to read its output.\n\n", h.RuntimeSkill, playbookTODO)
	}

	b.WriteString("## Verification command\n\n")
	if strings.TrimSpace(e.Baseline) != "" {
		fmt.Fprintf(&b, "`%s` (from `executor.baseline`, captured once per executor run so a worker can\ntell NEW failures from pre-existing ones).\n\n", e.Baseline)
		b.WriteString(playbookTODO + " what this command does NOT cover (test suites outside it, checks that\nonly run in CI) and how to run those when a claim depends on them.\n\n")
	} else {
		b.WriteString(playbookTODO + " the project's full verification command. Consider setting it as\n`executor.baseline` too - then every worker is told up front which failures\nwere already there before it started.\n\n")
	}
	b.WriteString("### Known red on the base branch - never count these against a branch\n\n")
	b.WriteString(playbookTODO + " list pre-existing failures here, and re-confirm the list before leaning\non it (run the same command on the base branch). Delete entries that are gone.\n\n")

	b.WriteString("## PR conventions\n\n")
	b.WriteString(playbookTODO + " base branch, merge style, title format, what belongs in the body, whether\na reviewer is added, and what happens to the ticket after merge.\n")

	return b.String()
}
