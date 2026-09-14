package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
