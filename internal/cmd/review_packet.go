package cmd

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// The review prompt has promised since 0.36.2 that each reviewer gets "ONLY the
// branch diff plus the AC". It was never true. What the worker actually sends is
// a 1.9-4.8kB instruction to go and look, and the reviewer then looks: the 16
// reviewers in epic orbit-106 made 674 tool calls and pulled 2.27M
// characters out of the repo (Read 1.35M, Bash 0.69M, Grep 0.20M) - ~42 calls
// and ~142kB each. A prompt cannot make an agent with a full toolset stop
// reading; only not having the tool can, and only having the material already
// makes that reasonable.
//
// So pm hands over the diff itself. The trade is ~142kB of tool output for
// ~15kB of prompt, and the two are not the same kind of token: a prompt is sent
// once and cached, while every tool result lands in the context permanently and
// is re-sent with every subsequent turn of that reviewer.

// reviewPacketBudget bounds the packet. Sized to land in the 10-20kB the ticket
// planned for a normal change while leaving room for a large one to degrade
// rather than be cut off mid-hunk.
const reviewPacketBudget = 48_000

// reviewPacketHeader introduces the packet to the reviewer. It says pm put it
// there, out loud and by name. This is not decoration: in the 96-1 hook probe a
// worker noticed text a hook had injected into a subagent prompt and flagged it
// as suspicious, which is the correct instinct - so anything pm adds to someone
// else's prompt has to announce itself as pm policy rather than read as the
// spawning agent's words.
const reviewPacketHeader = "\n\n---\n## The change under review (attached by pm, not by the agent that spawned you)\n\n" +
	"This is the complete diff of the work being reviewed, tests included, measured from the commit the run started at. " +
	"You have it already: do not go looking for it. Read a file only when the diff genuinely does not tell you enough, " +
	"and judge the change on what is here plus the criteria you were given. When you do read, read big files in slices, " +
	"never end-to-end: Grep for the symbol, then Read the surrounding range with offset/limit - a whole file over ~600 " +
	"lines rides in your context for every turn that follows and is billed again each time.\n\n"

// reviewDocPacketHeader replaces the header above when nothing but prose
// changed. The generic brief is a category error on a document: it sends an
// adversarial reviewer looking for correctness bugs, unhandled cases and style
// violations in text that has none of those, and what it finds instead is
// wording to argue about. What CAN be wrong in a document is what it claims
// about the code - and checking that is a bounded job that ends.
//
// This is the half of the doc-only regime that makes the single reviewer worth
// having rather than merely cheap. The reviewer keeps Read/Grep/Glob (only Bash
// went in 0.38.0), so it can resolve every path and symbol the document cites.
const reviewDocPacketHeader = "\n\n---\n## The document under review (attached by pm, not by the agent that spawned you)\n\n" +
	"This change is documentation only - no code changed - so pm has given you the document's full diff and a " +
	"narrower job than a code review. Review it as a DOCUMENT:\n\n" +
	"- Every factual claim it makes about this codebase has to hold. Resolve the paths, symbols, commands and line " +
	"references it cites, and report each one that does not exist or does not say what the document says it does.\n" +
	"- Report steps a reader could not actually follow: a script, file or command that is not there, or state the " +
	"document never establishes before relying on it.\n" +
	"- Report content copied from somewhere that already owns it (a skill, a README, another document), where a " +
	"reference would stay correct and a copy will not.\n" +
	"- Do NOT review wording, tone, structure or formatting, and do not propose rewrites.\n\n" +
	"Resolve citations in slices, not by whole-file reads: Grep for the cited symbol or path, then Read the " +
	"surrounding range with offset/limit - a whole file over ~600 lines rides in your context for every turn that " +
	"follows and is billed again each time.\n\n" +
	"pm allows ONE reviewer and ONE round for a documentation change, so this is the review: report what you " +
	"actually verified, and say plainly what you did not get to.\n\n"

const reviewPacketTruncNote = "\n\n(The full diff exceeded the size pm will put in a prompt. Above is the file-by-file summary " +
	"followed by the largest changed files in full. The files not shown in full are named in the summary - read those from " +
	"the working tree if a finding depends on them.)\n"

// gitOut runs a git command and returns its stdout. The error is deliberately
// ignored rather than propagated: `git diff --no-index` exits 1 precisely when
// it found differences, which is the case this code exists for, and every
// caller here already treats empty output as "nothing to say". A git that
// genuinely fails produces no output and degrades to no packet, which is the
// behaviour that existed before any of this.
func gitOut(dir string, args ...string) string {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, _ := c.Output()
	return string(out)
}

// untrackedFiles lists files the worker created and has not added. They are
// invisible to `git diff <base>` - which reads the index and the tree, not the
// directory - so without this a worker that spawns its reviewers before
// committing hands them a diff with its new files missing, and pm sizes the
// review as though those files did not exist. Gitignored files stay out
// (--exclude-standard): build output is not the change under review.
func untrackedFiles(dir string) []string {
	var names []string
	for _, line := range strings.Split(gitOut(dir, "ls-files", "--others", "--exclude-standard"), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// gitDiffAll is the worker's whole change since baseSHA: what git tracks, plus
// each untracked file diffed against nothing. flags are passed to both halves
// (--numstat, --stat), so the two agree on what they are reporting.
func gitDiffAll(dir, baseSHA string, flags ...string) string {
	var b strings.Builder
	b.WriteString(gitOut(dir, append(append([]string{"diff"}, flags...), baseSHA)...))
	for _, name := range untrackedFiles(dir) {
		b.WriteString(untrackedDiff(dir, name, flags...))
	}
	return b.String()
}

// untrackedDiff renders one untracked file as a diff against /dev/null. The
// numstat form comes out as "N\t0\t/dev/null => name", which parseNumstat
// already reads correctly - it has handled the " => " rename shape since it was
// written.
func untrackedDiff(dir, name string, flags ...string) string {
	args := append([]string{"diff", "--no-index"}, flags...)
	return gitOut(dir, append(args, os.DevNull, name)...)
}

// changedFilesBySize returns the changed paths ordered by how much they changed,
// largest first, together with whether each is untracked (which decides how its
// diff has to be fetched). Tests and lockfiles are NOT excluded: the exclusions
// in review_cap.go decide how many reviewers a change is worth, and this decides
// what they are shown - a test encoding the wrong behaviour is exactly what
// review has to catch, so it goes in the packet.
func changedFilesBySize(dir, baseSHA string) []changedFile {
	untracked := map[string]bool{}
	for _, name := range untrackedFiles(dir) {
		untracked[name] = true
	}
	var rows []changedFile
	for _, line := range strings.Split(gitDiffAll(dir, baseSHA, "--numstat"), "\n") {
		cols := strings.Split(strings.TrimSpace(line), "\t")
		if len(cols) < 3 {
			continue
		}
		name := cols[len(cols)-1]
		if i := strings.LastIndex(name, " => "); i >= 0 {
			name = strings.TrimSuffix(name[i+4:], "}")
		}
		added, _ := strconv.Atoi(cols[0])
		deleted, _ := strconv.Atoi(cols[1])
		rows = append(rows, changedFile{name: name, lines: added + deleted, untracked: untracked[name]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].lines > rows[j].lines })
	return rows
}

type changedFile struct {
	name      string
	lines     int
	untracked bool
}

// diffPacketBody renders the diff to send, degrading when the whole of it will
// not fit: a summary of every changed file, then as many whole files as the
// budget allows, largest first. Truncating the raw diff at N bytes was the
// obvious alternative and is worse - it ends mid-hunk, and a reviewer given
// half a hunk cannot tell whether what it is looking at is the whole change.
func diffPacketBody(dir, baseSHA string) string {
	full := gitDiffAll(dir, baseSHA)
	if strings.TrimSpace(full) == "" {
		return ""
	}
	if len(full) <= reviewPacketBudget {
		return full
	}
	var b strings.Builder
	b.WriteString(gitDiffAll(dir, baseSHA, "--stat"))
	b.WriteString("\n")
	for _, f := range changedFilesBySize(dir, baseSHA) {
		var one string
		if f.untracked {
			one = untrackedDiff(dir, f.name)
		} else {
			one = gitOut(dir, "diff", baseSHA, "--", f.name)
		}
		if one == "" || b.Len()+len(one) > reviewPacketBudget {
			continue
		}
		b.WriteString(one)
	}
	b.WriteString(reviewPacketTruncNote)
	return b.String()
}

// buildReviewPacket returns the text to append to a reviewer's prompt, or "" if
// there is nothing to attach.
func buildReviewPacket(dir, baseSHA string) string {
	if dir == "" || baseSHA == "" {
		return ""
	}
	body := diffPacketBody(dir, baseSHA)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	header := reviewPacketHeader
	if docOnlyDiff(dir, baseSHA) {
		header = reviewDocPacketHeader
	}
	return header + "```diff\n" + body + "\n```\n"
}

// docOnlyDiff reports whether every changed file is prose. It re-measures
// rather than taking the answer from the caller that already knows: the two
// call sites are a refusal and a rewrite, they run at different moments of the
// hook, and threading one boolean through the rewrite path to save a numstat
// that costs milliseconds would trade a real coupling for nothing.
func docOnlyDiff(dir, baseSHA string) bool {
	if dir == "" || baseSHA == "" {
		return false
	}
	_, _, docOnly := parseNumstat(gitDiffAll(dir, baseSHA, "--numstat"))
	return docOnly
}
