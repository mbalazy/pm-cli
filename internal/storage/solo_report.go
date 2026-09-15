package storage

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A solo shift's report split into the parts a reader acts on - the
// cockpit's report page and the home row. The report is the /solo skill's
// prose, but its skeleton is the skill's contract (prompts/report.md):
// "## 1. Co z taskami" with one block per task (a "### heading" or a
// "**heading**" line, then "Bug:", "Stan:", "Sprawdzone:", "Przed PR-em:"),
// "## 2. Decyzje podjęte za Ciebie", the cleanup and technical sections, and
// "## TL;DR" last. A part a report does not have (older shifts wrote
// "Pomysły na nowe tickety", some wrote no diacritics) stays empty - nothing
// is guessed, and the page always offers the whole report too.

// Task outcomes: the report's "Stan:" word, or the state file's Progress.
const (
	OutcomeDone      = "done"
	OutcomePartial   = "partial"
	OutcomeNotDone   = "not_done"
	OutcomeParked    = "parked"
	OutcomeUntouched = "untouched"
	OutcomeDoing     = "doing"
)

// ShiftReport is a report split into parts. Text fields are markdown.
type ShiftReport struct {
	Title string `json:"title,omitempty"`
	// Next is the TL;DR from the sentence addressed to the user on ("Od
	// Ciebie: ...", "Twój ruch: ..."); Summary is the TL;DR before it.
	Next      string       `json:"next,omitempty"`
	Summary   string       `json:"summary,omitempty"`
	Tasks     []ReportTask `json:"tasks"`
	Decisions []string     `json:"decisions"`
	Ideas     []string     `json:"ideas"`
	Cleanup   string       `json:"cleanup,omitempty"`
	Technical string       `json:"technical,omitempty"`
}

// ReportTask is one task block of the report's first section.
type ReportTask struct {
	Heading string `json:"heading"`
	Outcome string `json:"outcome,omitempty"`
	// Problem is "Bug:", State "Stan:" (for an untouched task, its "nie
	// ruszone, bo ..." line), Checked "Sprawdzone:", BeforePR "Przed PR-em:".
	Problem  string `json:"problem,omitempty"`
	State    string `json:"state,omitempty"`
	Checked  string `json:"checked,omitempty"`
	BeforePR string `json:"before_pr,omitempty"`
}

// ShiftSummary is what a list needs of a shift: a name, the outcome counts
// and the one thing the user is asked to do. Plain text.
type ShiftSummary struct {
	Title     string `json:"title,omitempty"`
	Done      int    `json:"done"`
	Partial   int    `json:"partial"`
	NotDone   int    `json:"not_done"`
	Parked    int    `json:"parked"`
	Untouched int    `json:"untouched"`
	Next      string `json:"next,omitempty"`
}

// ShiftNextMax caps the summary's next action, in runes: it is a row's
// reason, not the report.
const ShiftNextMax = 180

var (
	reportTitlePrefix = regexp.MustCompile(`(?i)^raport:\s*`)
	reportField       = regexp.MustCompile(`^(Bug|Stan|Sprawdzone|Przed PR-em)(\s+\S+)?:\s*(.*)$`)
	reportListItem    = regexp.MustCompile(`^(?:[-*]|\d+[.)])\s+(.*)$`)
	// nextMarker opens the TL;DR's sentence addressed to the user, in the
	// spellings the skill has written (2026-09-09..14).
	nextMarker = regexp.MustCompile(`(?i)(od ciebie|tw[oó]j (?:nast[eę]pny )?(?:ruch|krok)|twoja robota|do zrobienia po twojej stronie|do rana|nast[eę]pny (?:ruch|krok))`)
	plFold     = strings.NewReplacer("ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z")
)

// foldPL lowercases and drops Polish diacritics, so "Sprzątanie" and the
// "Sprzatanie" a keyboard without them wrote read the same.
func foldPL(s string) string { return plFold.Replace(strings.ToLower(s)) }

// ParseShiftReport splits a report's markdown into its parts.
func ParseShiftReport(md string) *ShiftReport {
	rep := &ShiftReport{Tasks: []ReportTask{}, Decisions: []string{}, Ideas: []string{}}
	type section struct {
		kind  string
		lines []string
	}
	var sections []*section
	var cur *section
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# ") && cur == nil && rep.Title == "":
			rep.Title = reportTitlePrefix.ReplaceAllString(strings.TrimSpace(line[2:]), "")
		case strings.HasPrefix(line, "## "):
			cur = &section{kind: reportSectionKind(line[3:])}
			sections = append(sections, cur)
		case cur != nil:
			cur.lines = append(cur.lines, line)
		}
	}
	for _, s := range sections {
		switch s.kind {
		case "tasks":
			rep.Tasks = append(rep.Tasks, parseReportTasks(s.lines)...)
		case "decisions":
			rep.Decisions = append(rep.Decisions, parseReportList(s.lines)...)
		case "ideas":
			rep.Ideas = append(rep.Ideas, parseReportList(s.lines)...)
		case "cleanup":
			rep.Cleanup = strings.TrimSpace(strings.Join(s.lines, "\n"))
		case "technical":
			rep.Technical = strings.TrimSpace(strings.Join(s.lines, "\n"))
		case "tldr":
			rep.Summary, rep.Next = splitNext(paragraphs(s.lines))
		}
	}
	return rep
}

func reportSectionKind(heading string) string {
	h := foldPL(heading)
	switch {
	case strings.Contains(h, "co z task") || strings.Contains(h, "co z zadani"):
		return "tasks"
	case strings.Contains(h, "decyzj"):
		return "decisions"
	case strings.Contains(h, "pomysl"):
		return "ideas"
	case strings.Contains(h, "sprzatani"):
		return "cleanup"
	case strings.Contains(h, "szczegol"):
		return "technical"
	case strings.Contains(h, "tl;dr") || strings.Contains(h, "tldr"):
		return "tldr"
	}
	return ""
}

// parseReportTasks reads the task blocks. A block starts at a "### " line or
// a line that is one bold phrase; its fields are the labelled lines (bullet,
// bold label or bold label-and-word, all three spellings exist); indented
// and unlabelled lines continue the last field. A top-level line saying
// "nie ruszone" is an untouched task of its own ("- X - nie ruszone, bo",
// "**X**: nie ruszone, bo").
func parseReportTasks(lines []string) []ReportTask {
	type block struct {
		ReportTask
		pre   string
		field *string
	}
	var blocks []*block
	var cur *block
	gap := false
	add := func(dst *string, text string) {
		switch {
		case *dst == "":
			*dst = text
		case gap:
			*dst += "\n\n" + text
		default:
			*dst += "\n" + text
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			gap = true
			continue
		}
		topLevel := !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t")
		plain := strings.TrimSpace(strings.ReplaceAll(strings.TrimPrefix(trimmed, "- "), "**", ""))
		untouchedAt := strings.Index(strings.ToLower(plain), "nie ruszone")
		m := reportField.FindStringSubmatch(plain)
		switch {
		case strings.HasPrefix(trimmed, "### "):
			cur = &block{}
			cur.Heading = strings.TrimSpace(strings.ReplaceAll(trimmed[4:], "**", ""))
			blocks = append(blocks, cur)
		case topLevel && m != nil && cur != nil:
			dst := fieldOf(&cur.ReportTask, m[1])
			if *dst != "" {
				gap = true // "Stan uzupełnienie:" adds a paragraph to Stan
			}
			add(dst, m[3])
			cur.field = dst
		case topLevel && untouchedAt > 0 && (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "**")):
			u := &block{}
			u.Heading = strings.TrimRight(strings.TrimSpace(plain[:untouchedAt]), " -–—:,")
			u.State, u.Outcome = plain[untouchedAt:], OutcomeUntouched
			blocks = append(blocks, u)
			cur = nil
		case topLevel && strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**"):
			cur = &block{}
			cur.Heading = plain
			blocks = append(blocks, cur)
		case cur != nil && cur.field != nil:
			add(cur.field, strings.TrimPrefix(line, "  "))
		case cur != nil:
			add(&cur.pre, trimmed)
		}
		gap = false
	}
	out := []ReportTask{}
	for _, b := range blocks {
		t := b.ReportTask
		if t.State == "" && strings.Contains(strings.ToLower(b.pre), "nie ruszone") {
			t.State, t.Outcome = b.pre, OutcomeUntouched
		}
		if t.State == "" && t.Problem == "" && t.Checked == "" && t.BeforePR == "" {
			continue // a grouping heading ("### Zadania nie ruszone")
		}
		if t.Outcome == "" {
			t.Outcome = stateOutcome(t.State)
		}
		out = append(out, t)
	}
	return out
}

func fieldOf(t *ReportTask, label string) *string {
	switch label {
	case "Bug":
		return &t.Problem
	case "Stan":
		return &t.State
	case "Sprawdzone":
		return &t.Checked
	}
	return &t.BeforePR
}

// stateOutcome reads the verdict word "Stan:" opens with: naprawione /
// zrobione, częściowo (also inside the first words: "naprawione (częściowo
// w jednym punkcie)"), nie naprawione / nie zrobione.
func stateOutcome(state string) string {
	f := foldPL(strings.TrimSpace(state))
	head := f
	if len(head) > 60 {
		head = head[:60]
	}
	switch {
	case strings.HasPrefix(f, "nie ruszone"):
		return OutcomeUntouched
	case strings.HasPrefix(f, "nie "):
		return OutcomeNotDone
	case strings.Contains(head, "czesciowo"):
		return OutcomePartial
	case strings.HasPrefix(f, "zrobion"), strings.HasPrefix(f, "naprawion"), strings.HasPrefix(f, "gotow"), strings.HasPrefix(f, "dowiezion"):
		return OutcomeDone
	}
	return ""
}

// parseReportList reads a section of bullets (or numbered items) into one
// string per item; an indented or unlabelled line continues the item, a
// paragraph after a blank line is an item of its own, "Brak." is none.
func parseReportList(lines []string) []string {
	items := []string{}
	gap := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			gap = true
			continue
		case strings.HasPrefix(trimmed, "#"):
		case !strings.HasPrefix(line, " ") && reportListItem.MatchString(line):
			items = append(items, strings.TrimSpace(reportListItem.FindStringSubmatch(line)[1]))
		case len(items) > 0 && (!gap || strings.HasPrefix(line, " ")):
			sep := "\n"
			if gap {
				sep = "\n\n"
			}
			items[len(items)-1] += sep + strings.TrimPrefix(line, "  ")
		case strings.Trim(foldPL(trimmed), ".") != "brak":
			items = append(items, trimmed)
		}
		gap = false
	}
	return items
}

// paragraphs joins a section's lines into paragraphs (a hard-wrapped TL;DR
// reads as one), separated by a blank line.
func paragraphs(lines []string) string {
	var paras []string
	var cur []string
	for _, line := range lines {
		if t := strings.TrimSpace(line); t != "" {
			cur = append(cur, t)
			continue
		}
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	if len(cur) > 0 {
		paras = append(paras, strings.Join(cur, " "))
	}
	return strings.Join(paras, "\n\n")
}

func splitNext(text string) (summary, next string) {
	loc := nextMarker.FindStringIndex(text)
	if loc == nil {
		return text, ""
	}
	return strings.TrimSpace(text[:loc[0]]), strings.TrimSpace(text[loc[0]:])
}

// summarizeShift counts the outcomes off the report when its tasks carry
// them, else off the state file's Progress; nil when there is nothing.
func summarizeShift(sh Shift, rep *ShiftReport) *ShiftSummary {
	s := &ShiftSummary{}
	if rep != nil {
		s.Title = plainLine(reportDisplayTitle(rep), ShiftNextMax)
		s.Next = plainLine(rep.Next, ShiftNextMax)
		for _, t := range rep.Tasks {
			s.add(t.Outcome)
		}
	}
	if s.Counts() == "" {
		for _, t := range sh.Tasks {
			s.add(t.Outcome)
		}
	}
	if *s == (ShiftSummary{}) {
		return nil
	}
	return s
}

func (s *ShiftSummary) add(outcome string) {
	switch outcome {
	case OutcomeDone:
		s.Done++
	case OutcomePartial:
		s.Partial++
	case OutcomeNotDone:
		s.NotDone++
	case OutcomeParked:
		s.Parked++
	case OutcomeUntouched:
		s.Untouched++
	}
}

// Counts words the outcome counts, zeros left out: "6 done · 1 untouched".
func (s *ShiftSummary) Counts() string {
	var parts []string
	for _, c := range []struct {
		n    int
		word string
	}{{s.Done, "done"}, {s.Partial, "partial"}, {s.NotDone, "not done"}, {s.Parked, "parked"}, {s.Untouched, "untouched"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.word))
		}
	}
	return strings.Join(parts, " · ")
}

// Unfinished reports whether a task came back partial, not done or parked.
func (s *ShiftSummary) Unfinished() bool { return s.Partial+s.NotDone+s.Parked > 0 }

// reportDisplayTitle names a shift by its report: the report's own title
// unless it only says "raport zmiany solo <date>", else the task headings.
func reportDisplayTitle(rep *ShiftReport) string {
	if rep.Title != "" && !genericReportTitle(rep.Title) {
		return rep.Title
	}
	var heads []string
	for _, t := range rep.Tasks {
		if t.Heading != "" && t.Outcome != OutcomeUntouched {
			heads = append(heads, t.Heading)
		}
	}
	switch len(heads) {
	case 0:
		return ""
	case 1:
		return heads[0]
	}
	return fmt.Sprintf("%s (+%d more)", heads[0], len(heads)-1)
}

func genericReportTitle(title string) bool {
	f := foldPL(title)
	for _, w := range []string{"solo", "zmian", "raport", "report", "shift"} {
		if strings.Contains(f, w) {
			return true
		}
	}
	return false
}

// plainLine turns markdown into one line of plain text of at most max
// runes, cut at a word.
func plainLine(md string, max int) string {
	s := strings.Join(strings.Fields(strings.NewReplacer("`", "", "**", "").Replace(md)), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}
