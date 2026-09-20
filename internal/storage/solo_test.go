package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func writeShift(t *testing.T, projectDir, name, body string, report bool) {
	t.Helper()
	dir := filepath.Join(projectDir, ShiftDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if report {
		if err := os.WriteFile(filepath.Join(dir, name+"-report.md"), []byte("# report "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadShifts(t *testing.T) {
	dir := t.TempDir()
	// The spellings the skill has actually written.
	writeShift(t, dir, "2026-09-08-aaaa", "# solo aaaa\n\n## Status: closed 2026-09-08T10:14+0200\n\n## Queue\nsolo-1 · first task · done · runtime no\n(a note)\n\n## Progress\nsolo-1 · 2026-09-08 · x\n", true)
	writeShift(t, dir, "2026-09-07-bbbb", "# night-shift bbbb\n## Status: closed 2026-09-07 19:20 (opened 17:55:39)\n## Queue\nsolo-2 · second · todo · x\nsolo-3 · third · doing\n", false)
	writeShift(t, dir, "2026-09-09-cccc", "# solo cccc\n## Status: open\n## Queue\nsolo-4 · fourth · todo\n", false)

	shifts := ReadShifts(dir, "solo")
	if len(shifts) != 3 {
		t.Fatalf("shifts = %+v", shifts)
	}
	byID := map[string]Shift{}
	for _, s := range shifts {
		byID[s.ID] = s
	}
	a := byID["aaaa"]
	if a.Kind != "solo" || a.Open || a.Report == "" || len(a.Tasks) != 1 || a.Tasks[0].Status != "done" || a.Date != "2026-09-08" {
		t.Errorf("aaaa = %+v", a)
	}
	if at, ok := ParseStamp(a.Closed); !ok || !at.Equal(time.Date(2026, 9, 8, 8, 14, 0, 0, time.UTC)) {
		t.Errorf("aaaa closed = %q", a.Closed)
	}
	b := byID["bbbb"]
	if b.Kind != "night-shift" || b.Open || b.Report != "" || len(b.Tasks) != 2 {
		t.Errorf("bbbb = %+v", b)
	}
	if at, ok := ParseStamp(b.Closed); !ok || at.Hour() != 19 {
		t.Errorf("bbbb closed = %q", b.Closed)
	}
	if c := byID["cccc"]; !c.Open || c.Closed != "" {
		t.Errorf("cccc = %+v", c)
	}
	if ReadShifts(t.TempDir(), "x") != nil {
		t.Error("no .shift dir must be no shifts")
	}
}

// sampleReport carries every spelling the /solo skill's reports have used
// for a task block (2026-09-09..14): "### heading" with bold labels, a bold
// heading line with bullet labels and nested bullets, bold label-and-word,
// "Stan uzupełnienie", and the three ways an untouched task is written.
const sampleReport = "# Raport zmiany solo - 2026-09-14\n\n## 1. Co z taskami\n\n" +
	"### ACME-1 - lista nie pokazuje klienta\n\n**Bug:** klient znika z listy.\n\n**Stan:** **naprawione** (częściowo w jednym punkcie). Lista pokazuje klienta.\n\n**Sprawdzone:** na symulatorze.\n\n**Przed PR-em:** nic.\n\n" +
	"**ACME-2, krok 2: ekran pozycji**\n- Bug: nie było ekranu.\n- Stan: **zrobione**. Ekran pokazuje dług.\n\n  Pod nim jest lista.\n- Sprawdzone: w przeglądarce.\n- Przed PR-em: scalić po kroku 1. Decyzje do potwierdzenia:\n  - Skala z konfiguracji.\n  - 404 to brak pozycji.\n\n" +
	"### ACME-3 - eksport\n\n- **Bug:** brak eksportu.\n- **Stan: nie zrobione.** Eksport nadal pada.\n- **Stan uzupełnienie:** przyczyna w API.\n\n" +
	"**ACME-4, krok 4: kontrakt**: nie ruszone, bo czeka na zamrożenie.\n\n" +
	"### Zadania nie ruszone\n\n- **ACME-5** - nie ruszone, bo czeka na makietę.\n\n" +
	"### ACME-6 - atrapa\n\nNie ruszone, bo czeka na ACME-3.\n\n" +
	"## 2. Decyzje podjete za Ciebie\n\n- Wybrałem A zamiast B.\n  Bo B psuje C.\n- Odrzuciłem D.\n\n" +
	"## 3. Pomysły na nowe tickety\n\n1. Ticket jeden.\n2. Ticket dwa.\n\n" +
	"## 4. Sprzatanie i stan runtime\n\n- serwer zatrzymany\n\n" +
	"## 5. Szczegóły techniczne\n\n`a1b2c3d` commit\n\n" +
	"## TL;DR\n\nZrobione dwa z trzech,\nnic nie wypchnięte. Twój ruch: wypchnij `ACME-2` i otwórz PR.\n"

func TestParseShiftReport(t *testing.T) {
	rep := ParseShiftReport(sampleReport)
	type want struct{ heading, outcome string }
	wants := []want{
		{"ACME-1 - lista nie pokazuje klienta", OutcomePartial},
		{"ACME-2, krok 2: ekran pozycji", OutcomeDone},
		{"ACME-3 - eksport", OutcomeNotDone},
		{"ACME-4, krok 4: kontrakt", OutcomeUntouched},
		{"ACME-5", OutcomeUntouched},
		{"ACME-6 - atrapa", OutcomeUntouched},
	}
	if len(rep.Tasks) != len(wants) {
		t.Fatalf("tasks = %+v", rep.Tasks)
	}
	for i, w := range wants {
		if got := rep.Tasks[i]; got.Heading != w.heading || got.Outcome != w.outcome {
			t.Errorf("task %d = %q %q, want %q %q", i, got.Heading, got.Outcome, w.heading, w.outcome)
		}
	}
	a1, a2, a3 := rep.Tasks[0], rep.Tasks[1], rep.Tasks[2]
	if a1.Problem != "klient znika z listy." || a1.Checked != "na symulatorze." || a1.BeforePR != "nic." {
		t.Errorf("ACME-1 fields = %+v", a1)
	}
	if a2.State != "zrobione. Ekran pokazuje dług.\n\nPod nim jest lista." || a2.BeforePR != "scalić po kroku 1. Decyzje do potwierdzenia:\n- Skala z konfiguracji.\n- 404 to brak pozycji." {
		t.Errorf("ACME-2 continuation = %q / %q", a2.State, a2.BeforePR)
	}
	if a3.State != "nie zrobione. Eksport nadal pada.\n\nprzyczyna w API." {
		t.Errorf("ACME-3 Stan uzupełnienie = %q", a3.State)
	}
	if rep.Tasks[3].State != "nie ruszone, bo czeka na zamrożenie." || rep.Tasks[5].State != "Nie ruszone, bo czeka na ACME-3." {
		t.Errorf("untouched = %q / %q", rep.Tasks[3].State, rep.Tasks[5].State)
	}
	if len(rep.Decisions) != 2 || rep.Decisions[0] != "Wybrałem A zamiast B.\nBo B psuje C." || rep.Decisions[1] != "Odrzuciłem D." {
		t.Errorf("decisions = %q", rep.Decisions)
	}
	if len(rep.Ideas) != 2 || rep.Ideas[1] != "Ticket dwa." {
		t.Errorf("ideas = %q", rep.Ideas)
	}
	if rep.Cleanup != "- serwer zatrzymany" || rep.Technical != "`a1b2c3d` commit" {
		t.Errorf("cleanup %q technical %q", rep.Cleanup, rep.Technical)
	}
	if rep.Summary != "Zrobione dwa z trzech, nic nie wypchnięte." || rep.Next != "Twój ruch: wypchnij `ACME-2` i otwórz PR." {
		t.Errorf("tl;dr = %q | %q", rep.Summary, rep.Next)
	}
	if got := ParseShiftReport("## 2. Decyzje podjęte za Ciebie\n\nBrak.\n"); len(got.Decisions) != 0 || len(got.Tasks) != 0 {
		t.Errorf("brak = %+v", got)
	}
}

func TestShiftProgressAndSummary(t *testing.T) {
	dir := t.TempDir()
	writeShift(t, dir, "2026-09-10-prog", "# solo prog\n## Status: closed 2026-09-10 14:01\n## Queue\nsolo-1 · a · todo\nsolo-2 · b · todo\nsolo-3 · c · todo\n"+
		"## Progress\n- solo-1 · done (local) · feat/a@abc1234 · 10 min\nsolo-2 * 13:34 * step 10 * parked: bramka * smoke/b@99c755e\n## Progress (cont.)\nsolo-3 · 21:22 · DONE (local, not pushed) · x/y@0123456789\n", false)
	sh := ReadShifts(dir, "p")[0]
	got := ""
	for _, tk := range sh.Tasks {
		got += tk.ID + "=" + tk.Outcome + "@" + tk.Branch + " "
	}
	if got != "solo-1=done@feat/a solo-2=parked@smoke/b solo-3=done@x/y " {
		t.Errorf("progress = %s", got)
	}
	if sh.Summary == nil || sh.Summary.Counts() != "2 done · 1 parked" || !sh.Summary.Unfinished() || sh.Summary.Title != "" {
		t.Errorf("summary off Progress = %+v", sh.Summary)
	}

	// With a report, the report's verdicts and its TL;DR win.
	if err := os.WriteFile(filepath.Join(dir, ShiftDir, "2026-09-10-prog-report.md"), []byte(sampleReport), 0o644); err != nil {
		t.Fatal(err)
	}
	s := ReadShifts(dir, "p")[0].Summary
	if s == nil || s.Counts() != "1 done · 1 partial · 1 not done · 3 untouched" || s.Title != "ACME-1 - lista nie pokazuje klienta (+2 more)" || s.Next != "Twój ruch: wypchnij ACME-2 i otwórz PR." {
		t.Errorf("summary off the report = %+v", s)
	}
	if long := plainLine(strings.Repeat("słowo ", 60), 40); utf8.RuneCountInString(long) > 41 || !strings.HasSuffix(long, "…") {
		t.Errorf("plainLine = %q", long)
	}
}

func TestAttentionSoloRowSaysWhatCameOut(t *testing.T) {
	store := attentionStore(t)
	writeShift(t, store.ProjectDir("solo"), "2026-09-08-out", "# solo out\n## Status: closed "+stampAgo(2*time.Hour)+"\n## Queue\nsolo-1 · waits on client · todo\n", false)
	if err := os.WriteFile(filepath.Join(store.ProjectDir("solo"), ShiftDir, "2026-09-08-out-report.md"), []byte(sampleReport), 0o644); err != nil {
		t.Fatal(err)
	}
	row := build(t, store, "", AttentionOptions{}).Section(SectionSoloReports).Rows[0]
	if row.Title != "ACME-1 - lista nie pokazuje klienta (+2 more)" || row.Severity != SeverityWarn ||
		row.Reason != "1 done · 1 partial · 1 not done · 3 untouched · Twój ruch: wypchnij ACME-2 i otwórz PR." {
		t.Errorf("row = %q | %q | %s", row.Title, row.Reason, row.Severity)
	}
}

func TestAttentionSoloReportsAndDismiss(t *testing.T) {
	store := attentionStore(t)
	writeShift(t, store.ProjectDir("solo"), "2026-09-08-fresh", "# solo fresh\n## Status: closed "+stampAgo(20*time.Hour)+"\n## Queue\nsolo-1 · waits on client · done\n", true)
	writeShift(t, store.ProjectDir("solo"), "2026-08-01-old", "# solo old\n## Status: closed "+stampAgo(30*24*time.Hour)+"\n## Queue\nsolo-2 · x · done\n", true)
	writeShift(t, store.ProjectDir("solo"), "2026-09-09-noreport", "# solo noreport\n## Status: closed "+stampAgo(time.Hour)+"\n", false)

	a := build(t, store, "", AttentionOptions{})
	sec := a.Section(SectionSoloReports)
	if sec == nil || len(sec.Rows) != 1 {
		t.Fatalf("solo_reports = %+v", sec)
	}
	row := sec.Rows[0]
	if row.Shift != "fresh" || row.Title != "solo-1 · waits on client" || !has(row.Actions, ActionOpenReport) || !has(row.Actions, ActionDismiss) {
		t.Errorf("row = %+v", row)
	}

	// Dismiss the solo row and the worst needs_me row.
	needs := a.Section(SectionNeedsMe)
	first := needs.Rows[0]
	if !has(first.Actions, ActionDismiss) {
		t.Fatalf("needs_me row without dismiss: %+v", first)
	}
	if err := AddDismissed(store.RootDir(), []string{row.DismissKey(), first.DismissKey()}, attentionNow); err != nil {
		t.Fatal(err)
	}
	a = build(t, store, "", AttentionOptions{})
	if s := a.Section(SectionSoloReports); len(s.Rows) != 0 || s.Dismissed != 1 {
		t.Errorf("after dismiss solo = %+v", s)
	}
	n := a.Section(SectionNeedsMe)
	if n.Dismissed != 1 || n.Total != len(needs.Rows)-1 || has(ids(n.Rows), first.TaskID) {
		t.Errorf("after dismiss needs_me = %d rows, dismissed %d", len(n.Rows), n.Dismissed)
	}
	// Scope recounts the hidden rows per project.
	if s := a.Scope("acme-zap", ""); s.Section(SectionNeedsMe).Dismissed != 0 && first.Project != "acme-zap" {
		t.Errorf("scoped dismissed = %d", s.Section(SectionNeedsMe).Dismissed)
	}
	if s := a.Scope(first.Project, ""); s.Section(SectionNeedsMe).Dismissed != 1 {
		t.Errorf("own project dismissed = %d", s.Section(SectionNeedsMe).Dismissed)
	}

	// A new condition (another stamp) is a new row: it comes back by itself.
	moved := first
	moved.Since = stampAgo(time.Minute)
	if moved.DismissKey() == first.DismissKey() {
		t.Error("the key must carry the stamp")
	}

	n2, err := RemoveDismissed(store.RootDir(), func(k string) bool { return k == row.DismissKey() })
	if err != nil || n2 != 1 {
		t.Fatalf("restore = %d, %v", n2, err)
	}
	if s := build(t, store, "", AttentionOptions{}).Section(SectionSoloReports); len(s.Rows) != 1 || s.Dismissed != 0 {
		t.Errorf("after restore = %+v", s)
	}
}

func TestAttentionHidesExecutorByDefault(t *testing.T) {
	a := build(t, attentionStore(t), "cockpit:\n  show_executor: false\n", AttentionOptions{})
	for _, name := range []string{SectionNeedsMe, SectionLandedNoPR, SectionInProgress} {
		sec := a.Section(name)
		if sec == nil || len(sec.Rows) != 0 || sec.Note == "" {
			t.Errorf("%s = %+v", name, sec)
		}
	}
	for _, g := range a.Groups {
		if g.Failed != 0 || g.Visual != 0 {
			t.Errorf("group %s counts the executor: %+v", g.Slug, g)
		}
	}
	if len(a.Section(SectionWaiting).Rows) == 0 {
		t.Error("waiting must not depend on the switch")
	}
}
